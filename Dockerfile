# Keep the builder version aligned with the go directive in go.mod.
FROM golang:1.25.6-alpine AS builder
WORKDIR /app
ENV GOTOOLCHAIN=local
COPY go.mod go.sum ./
RUN go mod download
COPY internal ./internal
COPY cmd/gateway ./cmd/gateway
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /gateway ./cmd/gateway

FROM scratch
WORKDIR /app
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=builder /gateway /app/gateway
USER 65532:65532
EXPOSE 8080
ENTRYPOINT ["/app/gateway"]
