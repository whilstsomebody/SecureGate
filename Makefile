.PHONY: run build test lint token services docker-up docker-down

run:
	go run ./cmd/gateway

build:
	go build -o bin/gateway ./cmd/gateway

test:
	go test ./...

lint:
	go vet ./...

# Usage: make token ROLE=ADMIN
token:
	go run ./cmd/token $(ROLE)

# Start all backend microservices (runs in background)
services:
	go run ./cmd/userservice &
	go run ./cmd/adminservice &
	go run ./cmd/paymentsservice

docker-up:
	docker compose up -d

docker-down:
	docker compose down
