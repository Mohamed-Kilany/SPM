# ── Stage 1: Build the Go binary ───────────────────────────────────────────
FROM golang:1.27.4-alpine AS builder

ARG VERSION=dev
ARG COMMIT=none
ARG BUILD_DATE=unknown

WORKDIR /build

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -trimpath \
    -ldflags="-s -w -X main.version=${VERSION} -X main.commit=${COMMIT} -X main.buildDate=${BUILD_DATE}" \
    -o /spm-api ./cmd/api

# ── Stage 2: Runtime (alpine — includes wget for healthchecks) ─────────────
FROM alpine:3.21

RUN apk add --no-cache wget ca-certificates

# API binary (schema migrations are embedded in the binary)
COPY --from=builder /spm-api /spm-api

EXPOSE 8080

ENTRYPOINT ["/spm-api"]
