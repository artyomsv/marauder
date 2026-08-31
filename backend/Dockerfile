# syntax=docker/dockerfile:1.7

# --- Build stage --------------------------------------------------------
FROM golang:1.27-alpine AS builder

RUN apk add --no-cache git ca-certificates build-base

WORKDIR /src

# Cache deps
COPY go.mod go.sum* ./
RUN go mod download

# Copy source
COPY . .

ARG VERSION=0.0.0-dev
ARG COMMIT=unknown
ARG BUILD_DATE=unknown

# TARGETARCH is auto-populated by BuildKit/buildx for each --platform it builds
# (e.g. "arm64"). Cross-compile the Go binary to match, so the arm64 image
# actually contains an arm64 binary instead of an amd64 one (issue #74). The
# :-amd64 default keeps a plain `docker build` (no buildx, TARGETARCH unset)
# working.
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=linux GOARCH=${TARGETARCH:-amd64} \
    go build -trimpath -ldflags="-s -w \
      -X github.com/artyomsv/marauder/backend/internal/version.Version=${VERSION} \
      -X github.com/artyomsv/marauder/backend/internal/version.Commit=${COMMIT} \
      -X github.com/artyomsv/marauder/backend/internal/version.BuildDate=${BUILD_DATE}" \
    -o /out/marauder ./cmd/server

# --- Runtime stage ------------------------------------------------------
FROM alpine:3.24

RUN apk add --no-cache ca-certificates tzdata wget \
 && addgroup -S -g 10001 marauder \
 && adduser  -S -u 10001 -G marauder marauder

USER marauder:marauder
WORKDIR /home/marauder

COPY --from=builder /out/marauder /usr/local/bin/marauder

EXPOSE 8679

HEALTHCHECK --interval=30s --timeout=3s --retries=3 \
  CMD wget -qO- http://localhost:8679/health || exit 1

ENTRYPOINT ["/usr/local/bin/marauder"]
