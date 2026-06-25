# SecureGate — API Gateway in Go

SecureGate is a production-style **API Gateway** built in Go, designed to secure and manage microservice traffic. It acts as a centralized entry point that handles authentication, authorization, rate limiting, resilience, and observability — so individual services don't have to.

*This project showcases real-world backend engineering and DevOps practices.*

---

## Features

| Feature | Details |
|---|---|
| **JWT Authentication** | HS256 validation; every token carries a `jti` claim |
| **RBAC Authorization** | Prefix-based role enforcement (USER / ADMIN) |
| **JWT Revocation** | Invalidate a token before expiry via Redis; `POST /auth/revoke` |
| **Redis Rate Limiting** | Atomic Lua script; per-client IP window (configurable) |
| **Circuit Breaker** | Per-service CLOSED → OPEN → HALF_OPEN state machine; 503 on open |
| **X-Request-ID** | Generated or forwarded on every request; echoed in response |
| **Structured Logging** | JSON via `log/slog`; every log line carries method, path, status, duration, request\_id |
| **Prometheus Metrics** | Request count, latency histogram, rate-limit blocks, auth failures |
| **Grafana Dashboards** | Pre-wired via Docker Compose |
| **Health Endpoint** | `GET /health` probes Redis and returns JSON status |
| **Graceful Shutdown** | SIGINT/SIGTERM handled; 10-second drain window |
| **Panic Recovery** | Unhandled panics caught and returned as 500 |

---

## Architecture

```
┌─────────────────────────────────────────────────────────────────────────┐
│                              CLIENT                                     │
└───────────────────────────────────┬─────────────────────────────────────┘
                                    │ HTTP
                                    ▼
┌─────────────────────────────────────────────────────────────────────────┐
│                        SecureGate  :8080                                │
│                                                                         │
│  ┌──────────────────────────────────────────────────────────────────┐   │
│  │                     Middleware Chain                             │   │
│  │                                                                  │   │
│  │  ┌─────────────────────┐   panic → 500                           │   │
│  │  │  RecoveryMiddleware │                                         │   │
│  │  └──────────┬──────────┘                                         │   │
│  │             │                                                    │   │
│  │  ┌──────────▼──────────┐   generate / forward X-Request-ID       │   │
│  │  │  RequestIDMiddleware│                                         │   │
│  │  └──────────┬──────────┘                                         │   │
│  │             │                                                    │   │
│  │  ┌──────────▼──────────┐   record latency + request count        │   │
│  │  │  MetricsMiddleware   │───────────────────────────► Prometheus │   |
│  │  └──────────┬──────────┘                                         │   │
│  │             │                                                    │   │
│  │  ┌──────────▼──────────┐   per-IP window via Lua script          │   │
│  │  │ RateLimitMiddleware  │◄──────────────────────────────── Redis │   |
│  │  └──────────┬──────────┘   429 if exceeded                       │   │
│  │             │                                                    │   │
│  │  ┌──────────▼──────────┐   validate JWT (HS256)                  │   │
│  │  │   AuthMiddleware     │   check revocation set in Redis        │   │
│  │  │                      │   RBAC prefix match                    │   │
│  │  └──────────┬──────────┘   401 / 403 on failure                  │   │
│  └─────────────┼────────────────────────────────────────────────────┘   │
│                │                                                        │
│  ┌─────────────▼────────────────────────────────────────────────────┐   │
│  │                       ProxyHandler                               │   │
│  │                                                                  │   │
│  │  ┌──────────────────────────────────────────────────────────┐    │   │
│  │  │              Circuit Breaker (per service)               │    │   │
│  │  │CLOSED ──(5 failures)──► OPEN ──(30s cooldown)──►HALF_OPEN|    │   │
│  │  └──────────────────────────────────────────────────────────┘    │   │
│  │                     │ strip prefix, forward                      │   │
│  └─────────────────────┼────────────────────────────────────────────┘   │
└────────────────────────┼────────────────────────────────────────────────┘
                         │
          ┌──────────────┼──────────────┐
          │              │              │
          ▼              ▼              ▼
   ┌────────────┐ ┌────────────┐ ┌────────────┐
   │  User Svc  │ │ Admin Svc  │ │Payments Svc│
   │  :9001     │ │  :9003     │ │  :9002     │
   └────────────┘ └────────────┘ └────────────┘


Public endpoints (bypass auth):
  GET  /metrics      → Prometheus scrape
  GET  /health       → Redis probe → {"status":"ok","redis":"ok"}
  POST /auth/revoke  → write jti to Redis revocation set
```

---

## Project Structure

```
SecureGate/
├── cmd/
│   ├── gateway/          # Main API gateway (port 8080)
│   ├── userservice/      # Sample user service (port 9001)
│   ├── adminservice/     # Sample admin service (port 9003)
│   ├── paymentsservice/  # Sample payments service (port 9002)
│   └── token/            # JWT token generator CLI
├── internal/
│   ├── auth/             # RBAC route→role map
│   ├── circuitbreaker/   # Thread-safe circuit breaker
│   ├── config/           # Config struct; loads .env + env vars
│   ├── metrics/          # Prometheus metric definitions
│   ├── middlewares/      # Auth, rate limit, metrics, recovery, request-ID
│   ├── proxy/            # Reverse proxy with circuit breaker integration
│   └── ratelimit/        # Redis client, Lua rate limiter, revocation store
├── configs/
│   └── prometheus.yml
├── .github/workflows/
│   └── ci.yml            # GitHub Actions: vet → test → build
├── docker-compose.yml    # Redis, Prometheus, Grafana
├── Dockerfile            # Multi-stage build → minimal Alpine image
└── Makefile
```

---

## Environment Variables

| Variable | Default | Description |
|---|---|---|
| `JWT_SECRET` | *(required)* | HMAC secret for JWT signing and validation |
| `REDIS_ADDR` | `127.0.0.1:6379` | Redis address |
| `RATE_LIMIT_COUNT` | `5` | Max requests per window per IP |
| `RATE_LIMIT_WINDOW_SECONDS` | `10` | Window size in seconds |
| `USER_SERVICE_ADDR` | `http://localhost:9001` | User service base URL |
| `ADMIN_SERVICE_ADDR` | `http://localhost:9003` | Admin service base URL |
| `PAYMENTS_SERVICE_ADDR` | `http://localhost:9002` | Payments service base URL |
| `GATEWAY_PORT` | `:8080` | Port the gateway listens on |

Create a `.env` file in the project root (see example below):

```env
JWT_SECRET=your_ultra_secure_secret_key
REDIS_ADDR=127.0.0.1:6379
```

---

## Setup and Run

### 1. Clone

```bash
git clone https://github.com/whilstsomebody/securegate.git
cd securegate
```

### 2. Start the observability + Redis stack

```bash
make docker-up
# starts Redis (6379), Prometheus (9090), Grafana (3000)
```

### 3. Start backend microservices

```bash
go run ./cmd/userservice &
go run ./cmd/adminservice &
go run ./cmd/paymentsservice &
```

### 4. Run the gateway

```bash
make run
# or: go run ./cmd/gateway
```

Gateway is live at `http://localhost:8080`.

---

## Makefile Targets

```bash
make run          # run the gateway
make build        # compile to bin/gateway
make test         # go test ./...
make lint         # go vet ./...
make token        # generate a USER token  (ROLE=ADMIN for admin token)
make docker-up    # start Redis + Prometheus + Grafana
make docker-down  # stop containers
```

---

## Usage

### Generate a JWT token

```bash
make token              # USER token
make token ROLE=ADMIN   # ADMIN token
```

Copy the printed token.

### Call a protected route

```bash
curl -H "Authorization: Bearer <TOKEN>" http://localhost:8080/users/hello
curl -H "Authorization: Bearer <TOKEN>" http://localhost:8080/admin/dashboard
curl -H "Authorization: Bearer <TOKEN>" http://localhost:8080/payments/checkout
```

### Health check

```bash
curl http://localhost:8080/health
# {"status":"ok","redis":"ok"}
```

### Revoke a token

```bash
curl -X POST http://localhost:8080/auth/revoke \
  -H "Content-Type: application/json" \
  -d '{"token":"<JWT>"}'
# {"status":"revoked"}
```

The token's `jti` is stored in Redis with a TTL equal to the token's remaining lifetime. Subsequent requests with that token receive `401 Token has been revoked`.

### Rate limiting

```bash
for i in {1..10}; do
  curl -s -o /dev/null -w "%{http_code}\n" \
    -H "Authorization: Bearer <TOKEN>" \
    http://localhost:8080/users/hello
done
# 200 200 200 200 200 429 429 429 429 429
```

---

## Route → Role Map

| Path prefix | Required role | Backend |
|---|---|---|
| `/users/*` | USER | port 9001 |
| `/admin/*` | ADMIN | port 9003 |
| `/payments/*` | ADMIN | port 9002 |
| `/metrics` | *(public)* | Prometheus scrape |
| `/health` | *(public)* | Redis probe |

---

## Monitoring

| Metric | Type | Labels |
|---|---|---|
| `securegate_requests_total` | Counter | path, method, status |
| `securegate_request_duration_seconds` | Histogram | path |
| `securegate_rate_limited_total` | Counter | path |
| `securegate_auth_failures_total` | Counter | path, reason |

- Prometheus UI: `http://localhost:9090`
- Metrics endpoint: `http://localhost:8080/metrics`
- Grafana: `http://localhost:3000` (admin / admin → add Prometheus datasource `http://prometheus:9090`)

<img width="1536" height="1024" alt="grafana_prometheus" src="https://github.com/user-attachments/assets/61ca484d-b61f-4cbc-a9aa-21579f12a052" />

---

## Docker Build

```bash
docker build -t securegate .
docker run -p 8080:8080 --env-file .env securegate
```

---

## Running Tests

```bash
make test
# or: go test ./...
```

Tests cover: circuit breaker state transitions, auth middleware (missing token, bad format, expired, forbidden role, valid, public paths), rate limit middleware (IP extraction from all proxy headers, allow/block via stub), panic recovery, proxy path rewriting, and RBAC rules. No live Redis required.
