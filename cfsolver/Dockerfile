# syntax=docker/dockerfile:1.7

# --- Build stage --------------------------------------------------------
FROM golang:1.23-alpine AS builder

RUN apk add --no-cache git ca-certificates build-base

WORKDIR /src
COPY go.mod go.sum* ./
RUN go mod download
COPY . .

RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -trimpath -ldflags="-s -w" -o /out/cfsolver .

# --- Runtime stage ------------------------------------------------------
# Chromium and its dependencies are heavy. We use Debian slim because it
# has reliable chromium packages and Alpine's chromium is hit-or-miss.
FROM debian:12-slim

RUN apt-get update && apt-get install -y --no-install-recommends \
    chromium \
    ca-certificates \
    fonts-liberation \
    fonts-noto-color-emoji \
    libnss3 \
    libxss1 \
    libgbm1 \
    libasound2 \
    wget \
 && rm -rf /var/lib/apt/lists/*

RUN groupadd -g 10003 cfsolver \
 && useradd -m -u 10003 -g 10003 -s /usr/sbin/nologin cfsolver

USER cfsolver:cfsolver
WORKDIR /home/cfsolver

COPY --from=builder /out/cfsolver /usr/local/bin/cfsolver

EXPOSE 9244

HEALTHCHECK --interval=30s --timeout=3s --retries=3 \
  CMD wget -qO- http://localhost:9244/health || exit 1

ENV CFSOLVER_ADDR=":9244"
ENTRYPOINT ["/usr/local/bin/cfsolver"]
