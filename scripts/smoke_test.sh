#!/usr/bin/env bash
set -euo pipefail

GATEWAY_URL="${GATEWAY_URL:-http://localhost:8080}"
MGMT_URL="${MGMT_URL:-http://127.0.0.1:9000}"

GREEN='\033[0;32m'
RED='\033[0;31m'
NC='\033[0m'

pass() {
  echo -e "${GREEN}✓ PASS:${NC} $1"
}

fail() {
  echo -e "${RED}✗ FAIL:${NC} $1"
  exit 1
}

echo "=== SecureGate Operational Smoke Test ==="
echo "Testing Gateway at ${GATEWAY_URL}"
echo "Testing Management at ${MGMT_URL}"
echo ""

# 1. Management Liveness
STATUS=$(curl -s -o /dev/null -w "%{http_code}" "${MGMT_URL}/health")
if [ "$STATUS" -eq 200 ]; then
  pass "Management /health returned 200 OK"
else
  fail "Management /health returned ${STATUS}, expected 200"
fi

# 2. Management Readiness
STATUS=$(curl -s -o /dev/null -w "%{http_code}" "${MGMT_URL}/ready")
if [ "$STATUS" -eq 200 ]; then
  pass "Management /ready returned 200 OK (Redis reachable)"
else
  fail "Management /ready returned ${STATUS}, expected 200"
fi

# 3. Management Metrics
METRICS=$(curl -s "${MGMT_URL}/metrics")
if echo "$METRICS" | grep -q "securegate_requests_total"; then
  pass "Management /metrics serves Prometheus metrics"
else
  fail "Management /metrics did not contain securegate_requests_total"
fi

# 4. Public Listener Isolation
STATUS=$(curl -s -o /dev/null -w "%{http_code}" "${GATEWAY_URL}/health")
if [ "$STATUS" -eq 404 ]; then
  pass "Public /health correctly isolated (404 Not Found)"
else
  fail "Public /health returned ${STATUS}, expected 404"
fi

STATUS=$(curl -s -o /dev/null -w "%{http_code}" "${GATEWAY_URL}/metrics")
if [ "$STATUS" -eq 404 ]; then
  pass "Public /metrics correctly isolated (404 Not Found)"
else
  fail "Public /metrics returned ${STATUS}, expected 404"
fi

STATUS=$(curl -s -o /dev/null -w "%{http_code}" "${GATEWAY_URL}/auth/revoke")
if [ "$STATUS" -eq 404 ]; then
  pass "Public /auth/revoke retired (404 Not Found)"
else
  fail "Public /auth/revoke returned ${STATUS}, expected 404"
fi

# 5. Unauthenticated Request
STATUS=$(curl -s -o /dev/null -w "%{http_code}" "${GATEWAY_URL}/users/hello")
if [ "$STATUS" -eq 401 ]; then
  pass "Unauthenticated request rejected with 401 Unauthorized"
else
  fail "Unauthenticated request returned ${STATUS}, expected 401"
fi

# 6. Generate Tokens
USER_TOKEN=$(go run ./cmd/token USER | grep -v 'Your JWT' | grep -v '^$' | tail -n 1)
ADMIN_TOKEN=$(go run ./cmd/token ADMIN | grep -v 'Your JWT' | grep -v '^$' | tail -n 1)

# 7. Authorized User Request
RESP=$(curl -s -w "\n%{http_code}" -H "Authorization: Bearer ${USER_TOKEN}" "${GATEWAY_URL}/users/hello")
CODE=$(echo "$RESP" | tail -n 1)
BODY=$(echo "$RESP" | sed '$d')

if [ "$CODE" -eq 200 ] && echo "$BODY" | grep -q "hello for user service"; then
  pass "USER token authorized on /users/hello (200 OK)"
else
  fail "USER request returned ${CODE}: ${BODY}"
fi

# 8. RBAC Forbidden Check
STATUS=$(curl -s -o /dev/null -w "%{http_code}" -H "Authorization: Bearer ${USER_TOKEN}" "${GATEWAY_URL}/admin/dashboard")
if [ "$STATUS" -eq 403 ]; then
  pass "USER token blocked from /admin/dashboard with 403 Forbidden"
else
  fail "USER token on admin endpoint returned ${STATUS}, expected 403"
fi

# 9. Admin Authorized Request
RESP=$(curl -s -w "\n%{http_code}" -H "Authorization: Bearer ${ADMIN_TOKEN}" "${GATEWAY_URL}/admin/dashboard")
CODE=$(echo "$RESP" | tail -n 1)
BODY=$(echo "$RESP" | sed '$d')

if [ "$CODE" -eq 200 ] && echo "$BODY" | grep -q "Welcome Admin"; then
  pass "ADMIN token authorized on /admin/dashboard (200 OK)"
else
  fail "ADMIN request returned ${CODE}: ${BODY}"
fi

echo ""
echo -e "${GREEN}All operational smoke tests passed successfully!${NC}"
