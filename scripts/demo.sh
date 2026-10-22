#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

GREEN='\033[0;32m'
BLUE='\033[0;34m'
YELLOW='\033[1;33m'
CYAN='\033[0;36m'
NC='\033[0m'

echo -e "${BLUE}========================================================================${NC}"
echo -e "${BLUE}        SecureGate — Starting One-Command Demo Stack                    ${NC}"
echo -e "${BLUE}========================================================================${NC}"

# 1. Ensure .env exists with JWT_SECRET and REDIS_ADDR
if [ ! -f .env ]; then
  echo -e "${YELLOW}Creating default .env for demo...${NC}"
  SECRET=$(openssl rand -hex 32 2>/dev/null || echo "demo-jwt-secret-key-32-bytes-minimum")
  cat <<EOF > .env
JWT_SECRET="${SECRET}"
REDIS_ADDR="127.0.0.1:6379"
GATEWAY_PORT=":8080"
MANAGEMENT_ADDR="127.0.0.1:9000"
EOF
fi

# 2. Check / Start Docker services (Redis, Prometheus, Grafana)
echo -e "${CYAN}Checking observability and Redis stack...${NC}"
if command -v docker >/dev/null 2>&1 && docker info >/dev/null 2>&1; then
  docker compose up -d redis prometheus grafana
  echo -e "${GREEN}✓ Redis, Prometheus, and Grafana are running.${NC}"
else
  echo -e "${YELLOW}Docker is not active. Checking if local Redis is running at 127.0.0.1:6379...${NC}"
fi

# 3. Compile or run sample backend services in background
echo -e "${CYAN}Starting sample backend microservices...${NC}"
go run ./cmd/userservice >/dev/null 2>&1 &
PID_USER=$!

go run ./cmd/adminservice >/dev/null 2>&1 &
PID_ADMIN=$!

go run ./cmd/paymentsservice >/dev/null 2>&1 &
PID_PAYMENTS=$!

cleanup() {
  echo ""
  echo -e "${YELLOW}Stopping SecureGate demo processes...${NC}"
  kill $PID_USER $PID_ADMIN $PID_PAYMENTS $PID_GATEWAY 2>/dev/null || true
  wait $PID_USER $PID_ADMIN $PID_PAYMENTS $PID_GATEWAY 2>/dev/null || true
  echo -e "${GREEN}✓ All demo services stopped cleanly.${NC}"
}
trap cleanup EXIT INT TERM

# 4. Start SecureGate Gateway in background
echo -e "${CYAN}Starting SecureGate Reverse Proxy...${NC}"
go run ./cmd/gateway &
PID_GATEWAY=$!

# 5. Wait for Gateway readiness
echo -e "${CYAN}Waiting for gateway readiness on http://127.0.0.1:9000/ready...${NC}"
READY=false
for i in {1..30}; do
  if curl -sf http://127.0.0.1:9000/ready >/dev/null 2>&1; then
    READY=true
    break
  fi
  sleep 0.5
done

if [ "$READY" = false ]; then
  echo -e "${YELLOW}Warning: Readiness check did not respond yet. Check if Redis is reachable.${NC}"
else
  echo -e "${GREEN}✓ Gateway is ready!${NC}"
fi

# 6. Generate Demo Tokens
USER_TOKEN=$(go run ./cmd/token USER 2>/dev/null | grep -v 'Your JWT' | grep -v '^$' | tail -n 1)
ADMIN_TOKEN=$(go run ./cmd/token ADMIN 2>/dev/null | grep -v 'Your JWT' | grep -v '^$' | tail -n 1)

echo ""
echo -e "${GREEN}========================================================================${NC}"
echo -e "${GREEN} 🚀 SecureGate One-Command Demo is LIVE!                                 ${NC}"
echo -e "${GREEN}========================================================================${NC}"
echo -e "  Public Reverse Proxy:  ${CYAN}http://localhost:8080${NC}"
echo -e "  Private Health:        ${CYAN}http://127.0.0.1:9000/health${NC}"
echo -e "  Private Readiness:     ${CYAN}http://127.0.0.1:9000/ready${NC}"
echo -e "  Prometheus Metrics:    ${CYAN}http://127.0.0.1:9000/metrics${NC}"
echo -e "  Prometheus UI:         ${CYAN}http://localhost:9090${NC}"
echo -e "  Grafana Dashboard:     ${CYAN}http://localhost:3000/d/securegate-overview${NC}"
echo -e "                         (Pre-provisioned with live metrics, no login required)"
echo ""
echo -e "${BLUE}--- Demo Testing Examples ---${NC}"
echo -e "1. Valid USER request:"
echo -e "   curl -H \"Authorization: Bearer ${USER_TOKEN}\" http://localhost:8080/users/hello"
echo ""
echo -e "2. USER blocked from ADMIN route (403 Forbidden):"
echo -e "   curl -H \"Authorization: Bearer ${USER_TOKEN}\" http://localhost:8080/admin/dashboard"
echo ""
echo -e "3. Valid ADMIN request:"
echo -e "   curl -H \"Authorization: Bearer ${ADMIN_TOKEN}\" http://localhost:8080/admin/dashboard"
echo ""
echo -e "4. Check metrics:"
echo -e "   curl http://127.0.0.1:9000/metrics | grep securegate_"
echo ""
echo -e "${YELLOW}Press Ctrl+C at any time to stop the demo.${NC}"
echo -e "${BLUE}========================================================================${NC}"

# Keep script running until user interrupts with Ctrl+C
wait $PID_GATEWAY
