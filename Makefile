.PHONY: run build test test-integration lint token services docker-up docker-down check-config version demo docker-demo smoke smoke-live release

run:
	go run ./cmd/gateway

build:
	go build -o bin/gateway ./cmd/gateway

check-config:
	go run ./cmd/gateway -check-config

version:
	go run ./cmd/gateway -version

test:
	go test ./...

test-integration:
	go test -v -race ./test/integration/...

smoke:
	go test -v -race ./test/smoke/...

smoke-live:
	./scripts/smoke_test.sh

demo:
	./scripts/demo.sh

docker-demo:
	docker compose -f docker-compose.demo.yml up

release:
	./scripts/release.sh $(VERSION)

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

