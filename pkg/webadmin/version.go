package webadmin

import (
	"encoding/json"
	"fmt"
	"net/http"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/ryc2077/m365plus/pkg/models"
	"github.com/ryc2077/m365plus/pkg/webadmin/outbound"
)

var (
	Version     = models.Version
	Commit      = "unknown"
	BuildTime   = "unknown"
	startedAt   = time.Now()
	updateCheck uint32
)

func (s *Server) version(w http.ResponseWriter, _ *http.Request) {
	jsonOut(w, map[string]any{"version": Version, "commit": Commit, "buildTime": BuildTime, "go": runtime.Version(), "uptimeSeconds": int(time.Since(startedAt).Seconds()), "accounts": len(s.tokens.List()), "proxyPool": len(outbound.ProxyPoolStatus())})
}

func (s *Server) update(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeOpenAIError(w, 405, "invalid_request_error", "method not allowed")
		return
	}
	current := strings.TrimPrefix(strings.TrimSpace(Version), "v")
	stable := current != "" && current != "dev"
	request, err := http.NewRequestWithContext(r.Context(), http.MethodGet, "https://api.github.com/repos/ryc2077/m365plus/releases/latest", nil)
	if err != nil {
		writeOpenAIError(w, http.StatusInternalServerError, "update_check_failed", err.Error())
		return
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("User-Agent", "m365plus/"+current)
	response, err := (&http.Client{Timeout: 10 * time.Second}).Do(request)
	if err != nil {
		writeOpenAIError(w, http.StatusBadGateway, "update_check_failed", "无法连接 GitHub 检查更新")
		return
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		writeOpenAIError(w, http.StatusBadGateway, "update_check_failed", fmt.Sprintf("GitHub 返回状态码 %d", response.StatusCode))
		return
	}
	var release struct {
		TagName string `json:"tag_name"`
		HTMLURL string `json:"html_url"`
	}
	if err := json.NewDecoder(response.Body).Decode(&release); err != nil {
		writeOpenAIError(w, http.StatusBadGateway, "update_check_failed", "无法解析 GitHub Release 信息")
		return
	}
	latest := strings.TrimPrefix(strings.TrimSpace(release.TagName), "v")
	available := stable && compareVersions(latest, current) > 0
	message := "当前已是最新版本"
	if !stable {
		message = "当前为开发版本，请对照最新正式版本判断是否需要更新"
	} else if available {
		message = fmt.Sprintf("发现新版本 v%s", latest)
	}
	jsonOut(w, map[string]any{
		"current": current, "latest": latest,
		"channel":         map[bool]string{true: "stable", false: "development"}[stable],
		"updateAvailable": available, "recommendUpdate": available,
		"releaseUrl": release.HTMLURL, "message": message,
	})
}

func compareVersions(left, right string) int {
	leftParts := strings.Split(strings.SplitN(left, "-", 2)[0], ".")
	rightParts := strings.Split(strings.SplitN(right, "-", 2)[0], ".")
	for index := 0; index < 3; index++ {
		leftValue, rightValue := 0, 0
		if index < len(leftParts) {
			leftValue, _ = strconv.Atoi(leftParts[index])
		}
		if index < len(rightParts) {
			rightValue, _ = strconv.Atoi(rightParts[index])
		}
		if leftValue < rightValue {
			return -1
		}
		if leftValue > rightValue {
			return 1
		}
	}
	return 0
}

func ReleaseTag() string {
	v := strings.TrimSpace(Version)
	if v == "" || v == "dev" {
		return ""
	}
	return fmt.Sprintf("stable-v%s", v)
}
