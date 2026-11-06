# =============================================================================
# Build stage
# =============================================================================
FROM golang:1.22-alpine AS builder

RUN apk add --no-cache ca-certificates

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -ldflags="-s -w" -o sehataqua-monitor .

# =============================================================================
# Runtime stage – distroless (digest-pinned for supply-chain security)
# =============================================================================
FROM gcr.io/distroless/base-debian12:nonroot@sha256:cefb62cf1f8272c57b478dd0d82cae40025aa6d1524c00155a05db761a9858f2

LABEL org.opencontainers.image.title="SehatAqua Stock Monitor"
LABEL org.opencontainers.image.description="Monitor loyalty reward stock and send Telegram notifications"
LABEL org.opencontainers.image.source="https://github.com/your-org/sehataqua-monitor"

COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=builder /app/sehataqua-monitor /usr/local/bin/sehataqua-monitor

# /tmp is writable in Cloud Run and is used for the default state file.
ENV STATE_FILE=/tmp/state.json

ENTRYPOINT ["/usr/local/bin/sehataqua-monitor"]
