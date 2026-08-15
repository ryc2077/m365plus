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

# Runtime stage
FROM alpine:3.24@sha256:28bd5fe8b56d1bd048e5babf5b10710ebe0bae67db86916198a6eec434943f8b

# Chromium enables the CDP fallback path for expired account token refresh.
RUN apk add --no-cache ca-certificates git go tzdata chromium
ENV CHROME_BIN=/usr/bin/chromium

WORKDIR /app

COPY --from=builder /build/bin/m365-bridge ./bin/m365-bridge
COPY web ./web

# Data directory for tokens, cache, setup.json
RUN mkdir -p data/tokens

EXPOSE 8000

ENTRYPOINT ["./bin/m365-bridge"]
CMD ["serve", "--port", "8000"]
