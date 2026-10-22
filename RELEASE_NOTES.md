# SecureGate v0.2.0 Release Notes

SecureGate v0.2.0 delivers the complete set of practical operational and developer-experience improvements, advancing SecureGate toward a lightweight, production-ready reverse proxy.

---

## Highlights

### 1. One-Command Complete Demo Stack (`make demo` & `make docker-demo`)
- Added `scripts/demo.sh` runnable via `make demo`: automatically verifies local Redis/Prometheus/Grafana, starts sample microservices (`userservice`, `adminservice`, `paymentsservice`), starts the Gateway, generates demonstration USER and ADMIN tokens, executes sample requests, and prints live Grafana dashboard links.
- Added `docker-compose.demo.yml` (`make docker-demo`): provides a 100% containerized one-command stack including the gateway, Redis, all sample backends, Prometheus, and Grafana.

### 2. Configuration Validation (`check-config` / `-check-config` / `-t`)
- Added configuration pre-flight checking that validates environment variables, `.env` file syntax, upstream URLs, port ranges, JWT settings, and resource limits without opening sockets or connecting to Redis.
- Available via:
  ```bash
  ./bin/gateway -check-config
  ./bin/gateway -t
  ./bin/gateway check-config
  make check-config
  ```
- Exits with status `0` on success with a structured summary of all validated parameters, or status `1` with descriptive diagnostic output on syntax or semantic errors.

### 3. Real Redis Integration Test Suite (`make test-integration`)
- Added real Redis integration tests in `internal/ratelimit/real_redis_test.go` and `test/integration/real_redis_test.go`.
- Validates the atomic Lua sliding-window rate limiter, TTL management, key expiration, and token revocation lookups against a live Redis server.
- Integrated Redis Alpine service containers into `.github/workflows/ci.yml` so integration tests run automatically in CI on every push and pull request.

### 4. Expanded Prometheus Observability Metrics
Extended `internal/metrics` with comprehensive operational instrumentation:
- `securegate_admission_rejections_total` (Counter, labels: `reason` = `request_limit` | `upgrade_limit`): Tracks requests rejected due to concurrency limits.
- `securegate_active_connections` (Gauge): Current open client connections on the public listener.
- `securegate_active_requests` (Gauge): Current concurrent requests being processed.
- `securegate_active_upgrades` (Gauge): Current active hijacked/WebSocket upgrade streams.
- `securegate_upstream_duration_seconds` (Histogram, labels: `route`): Measures roundtrip response duration to upstreams separately from client read/middleware time.
- `securegate_timeouts_total` (Counter, labels: `type` = `upstream_dial` | `upstream_header` | `upstream_read_idle` | `client_read_idle` | `client_write`): Classifies IO timeouts.
- `securegate_circuit_breaker_state` (Gauge, labels: `route`): Tracks breaker states (`0` = CLOSED, `1` = OPEN, `2` = HALF_OPEN).
- `securegate_build_info` (Gauge, labels: `version`, `commit`, `go_version`): Exposes build metadata.

### 5. Automated Grafana Provisioning
- Automatically provisions the Prometheus data source at `http://prometheus:9090` without manual setup (`configs/grafana/provisioning/datasources/datasource.yml`).
- Automatically provisions the pre-built **SecureGate Overview** dashboard (`configs/grafana/dashboards/securegate.json`) featuring:
  - Request throughput by route and HTTP status
  - p50, p95, and p99 latency percentiles + upstream latency
  - Active connections, requests, and upgrade gauges
  - Admission rejection and rate-limit counters
  - Authentication and RBAC failure tracking
  - Live circuit breaker status panels
  - Timeouts and aborted response rates
- Enables anonymous local viewer/editor access for instant evaluation in development.

### 6. Consistent JSON Error Responses & Documented Retry Semantics
- Standardized all gateway error responses to use a unified JSON schema (`httpio.ErrorResponse`):
  ```json
  {
    "error": {
      "code": "RATE_LIMIT_EXCEEDED",
      "message": "Too many requests to resolve",
      "status": 429,
      "request_id": "req-12345"
    }
  }
  ```
- Propagates `X-Request-ID` correlation identifiers in headers and JSON envelopes.
- Sets explicit `Retry-After` headers:
  - `429 Too Many Requests`: `Retry-After: <window_seconds>`
  - `503 Service Unavailable` (Circuit breaker tripped): `Retry-After: 30`
  - `503 Service Unavailable` (Proxy busy / overload): `Retry-After: 1`
  - `503 Service Unavailable` (Store failure / Redis unavailable): `Retry-After: 5`
- Documented upstream retry behavior: SecureGate intentionally avoids automatic retries on upstreams to prevent duplicate execution of non-idempotent methods and retry storms.

### 7. End-to-End Operational Smoke Tests (`make smoke` & `scripts/smoke_test.sh`)
- Implemented race-enabled operational smoke test suite (`test/smoke/smoke_test.go`) covering:
  - Management isolation vs public listener 404s
  - Missing and expired token rejection
  - Valid USER role routing and `X-Request-ID` propagation
  - RBAC forbidden enforcement (USER on admin routes)
  - Admin access to admin and payments routes
  - Immediate token revocation enforcement
  - Rate limiting burst rejection and `Retry-After` headers
  - Circuit breaker tripping after consecutive upstream failures
- Portable runner script `scripts/smoke_test.sh` for live environment validation.

### 8. Release Automation & Versioned Binaries (`make release`)
- Added `scripts/release.sh` to cross-compile static binaries with embedded version, commit, and build date metadata via `ldflags`:
  - `securegate-v0.2.0-linux-amd64`
  - `securegate-v0.2.0-linux-arm64`
  - `securegate-v0.2.0-darwin-amd64`
  - `securegate-v0.2.0-darwin-arm64`
- Automatically generates and validates `SHA256SUMS`.
- Added `-version` and `-v` CLI flags and startup version logging.

### 9. Validated Container Build & Minimal Non-Root Runtime
- Verified multi-stage Docker build producing a minimal `scratch` container image running as non-root user `65532:65532`.
- Validated read-only container filesystem operation and containerized configuration testing.

---

## Upgrade Guide from v0.1.0

1. **Error Parsing**: Clients should now expect `Content-Type: application/json; charset=utf-8` on gateway errors, parsing the `error` object (`code`, `message`, `status`, `request_id`).
2. **Retry Headers**: Automated clients should honor the `Retry-After` response header on `429` and `503` responses before attempting retries.
3. **Observability**: Point Prometheus to `127.0.0.1:9000/metrics` or use the pre-configured Grafana dashboard on `127.0.0.1:3000`.
4. **Pre-flight Checks**: Add `gateway -check-config` or `make check-config` to deployment health and validation scripts.
