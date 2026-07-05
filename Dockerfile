# ── Build stage ──────────────────────────────────────────────
FROM golang:1.22-alpine AS builder

WORKDIR /build

COPY go.mod ./
COPY main.go ./
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /metrics-collector .

# ── Runtime stage ───────────────────────────────────────────
FROM alpine:3.20

RUN apk --no-cache add ca-certificates

COPY --from=builder /metrics-collector /usr/local/bin/metrics-collector

ENTRYPOINT ["metrics-collector"]
