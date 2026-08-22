package servers

import (
	"context"
	"errors"
	"net"
	"net/http"
	"strconv"
	"strings"

	"github.com/ryc2077/m365plus/pkg/auth"
	"github.com/ryc2077/m365plus/pkg/client"
	"github.com/ryc2077/m365plus/pkg/logging"
)

const (
	upstreamAuthFailedCode     = "upstream_auth_failed"
	upstreamForbiddenCode      = "insufficient_permissions"
	upstreamRateLimitCode      = "rate_limit_exceeded"
	upstreamTimeoutCode        = "upstream_timeout"
	upstreamUnavailableCode    = "upstream_unavailable"
	upstreamRejectedCode       = "upstream_error"
	upstreamTurnFailedCode     = "upstream_turn_failed"
	internalProcessingCode     = "internal_error"
	rateLimitRetryAfterSeconds = 60
)

func openAIErrorType(status int) string {
	switch {
	case status == http.StatusUnauthorized, status == http.StatusForbidden:
		return "authentication_error"
	case status == http.StatusTooManyRequests:
		return "rate_limit_error"
	case status >= 400 && status < 500:
		return "invalid_request_error"
	default:
		return "server_error"
	}
}

func openAIErrorCode(status int) string {
	text := http.StatusText(status)
	if text == "" {
		return "error"
	}
	return strings.ToLower(strings.ReplaceAll(text, " ", "_"))
}

func classifyUpstreamError(err error) (int, string) {
	switch {
	case errors.Is(err, auth.ErrTokenNotFound), errors.Is(err, auth.ErrRefreshFailed):
		return http.StatusUnauthorized, upstreamAuthFailedCode
	case errors.Is(err, context.DeadlineExceeded):
		return http.StatusGatewayTimeout, upstreamTimeoutCode
	case errors.Is(err, client.ErrHandshakeFailed), errors.Is(err, client.ErrConnectionClosed):
		return http.StatusBadGateway, upstreamUnavailableCode
	}

	if netErr, ok := errors.AsType[net.Error](err); ok && netErr.Timeout() {
		return http.StatusGatewayTimeout, upstreamTimeoutCode
	}
	if _, ok := client.TurnFailure(err); ok {
		return http.StatusBadGateway, upstreamTurnFailedCode
	}
	if errors.Is(err, client.ErrEmptyTurn) {
		return http.StatusBadGateway, upstreamTurnFailedCode
	}
	if status, ok := client.UpstreamStatus(err); ok {
		switch status {
		case http.StatusUnauthorized:
			return http.StatusUnauthorized, upstreamAuthFailedCode
		case http.StatusForbidden:
			return http.StatusForbidden, upstreamForbiddenCode
		case http.StatusPaymentRequired, http.StatusTooManyRequests:
			return http.StatusTooManyRequests, upstreamRateLimitCode
		case http.StatusServiceUnavailable:
			return http.StatusServiceUnavailable, upstreamUnavailableCode
		case http.StatusGatewayTimeout, http.StatusRequestTimeout:
			return http.StatusGatewayTimeout, upstreamTimeoutCode
		default:
			return http.StatusBadGateway, upstreamRejectedCode
		}
	}
	return http.StatusInternalServerError, internalProcessingCode
}

func upstreamErrorMessage(op, code string) string {
	switch code {
	case upstreamAuthFailedCode:
		return "M365 authentication failed for this " + op + " request"
	case upstreamForbiddenCode:
		return "M365 refused this " + op + " request for the selected account"
	case upstreamRateLimitCode:
		return "M365 rate limit reached for this " + op + " request; retry after the interval in the Retry-After header"
	case upstreamTimeoutCode:
		return "M365 did not answer the " + op + " request in time"
	case upstreamUnavailableCode:
		return "M365 is currently unreachable for this " + op + " request"
	case upstreamRejectedCode:
		return "M365 rejected the " + op + " request"
	case upstreamTurnFailedCode:
		return "M365 accepted the " + op + " request but ended the turn without producing an answer"
	default:
		return "the " + op + " request failed before it could be completed"
	}
}

func streamErrorFields(op string, err error) (status int, code, message string) {
	status, code = classifyUpstreamError(err)
	logging.Errorf("%s stream failed: status=%d code=%s err=%v", op, status, code, err)
	return status, code, upstreamErrorMessage(op, code)
}

func (api *APIServer) sendErrorCode(w http.ResponseWriter, status int, code, message string) {
	api.sendJSON(w, status, map[string]any{
		"error": map[string]any{
			"message": message,
			"type":    openAIErrorType(status),
			"code":    code,
		},
	})
}

func (api *APIServer) sendUpstreamError(w http.ResponseWriter, op string, err error) {
	status, code := classifyUpstreamError(err)
	logging.Errorf("%s failed: status=%d code=%s err=%v", op, status, code, err)
	if status == http.StatusTooManyRequests {
		w.Header().Set("Retry-After", strconv.Itoa(rateLimitRetryAfterSeconds))
	}
	api.sendErrorCode(w, status, code, upstreamErrorMessage(op, code))
}
