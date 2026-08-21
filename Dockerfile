# Build stage
FROM golang:1.26-alpine@sha256:0178a641fbb4858c5f1b48e34bdaabe0350a330a1b1149aabd498d0699ff5fb2 AS builder

ARG VERSION=dev

WORKDIR /build

# Go module proxy (override at build time with --build-arg if needed)
ARG GOPROXY_URL=https://proxy.golang.org,direct
ENV GOPROXY=${GOPROXY_URL}

# Cache dependencies
COPY go.mod go.sum ./
RUN go mod download

# Copy source and build
COPY . .
RUN CGO_ENABLED=0 go build -ldflags "-X github.com/ryc2077/m365plus/pkg/models.Version=${VERSION}" -o bin/m365-bridge ./cmd/cli

# Reuse the official multi-architecture sing-box binary. Buildx automatically
# selects the matching linux/amd64 or linux/arm64 image for each target.
FROM ghcr.io/sagernet/sing-box:latest AS singbox

# Runtime stage
FROM alpine:3.24@sha256:28bd5fe8b56d1bd048e5babf5b10710ebe0bae67db86916198a6eec434943f8b

# Chromium enables the CDP fallback path for expired account token refresh.
RUN apk add --no-cache ca-certificates git go tzdata chromium
ENV CHROME_BIN=/usr/bin/chromium

WORKDIR /app

COPY --from=builder /build/bin/m365-bridge ./bin/m365-bridge
COPY --from=singbox /usr/local/bin/sing-box /usr/local/bin/sing-box
COPY web ./web
COPY scripts/docker-entrypoint.sh /usr/local/bin/m365plus-entrypoint

# Data directory for tokens, cache, setup.json
RUN mkdir -p data/tokens data/sing-box && chmod +x /usr/local/bin/m365plus-entrypoint

EXPOSE 8000

ENTRYPOINT ["/usr/local/bin/m365plus-entrypoint"]
CMD ["serve", "--port", "8000"]
