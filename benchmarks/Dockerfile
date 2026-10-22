FROM golang:1.25.6-alpine AS builder
WORKDIR /app
ENV GOTOOLCHAIN=local
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/benchmark ./cmd/benchmark
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /benchmark ./cmd/benchmark

FROM alpine:3.21
COPY --from=builder /benchmark /benchmark
ENTRYPOINT ["/benchmark"]
