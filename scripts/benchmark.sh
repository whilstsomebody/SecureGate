#!/usr/bin/env sh
# Run the comparison stack and collect one Docker resource sample per second.
set -eu

root_dir=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$root_dir"

compose() {
  docker compose -f benchmarks/docker-compose.yml "$@"
}

cleanup() {
  compose down --remove-orphans
}

trap cleanup EXIT INT TERM
mkdir -p benchmarks/results
: > benchmarks/results/resources.jsonl

compose build
compose up -d upstream securegate nginx

container_ids="$(compose ps -q securegate) $(compose ps -q nginx)"
compose run --rm benchmark &
benchmark_pid=$!

(
  while kill -0 "$benchmark_pid" 2>/dev/null; do
    timestamp=$(date -u +%Y-%m-%dT%H:%M:%SZ)
    docker stats --no-stream \
      --format "{\"timestamp\":\"$timestamp\",\"container\":\"{{.Name}}\",\"cpu_percent\":\"{{.CPUPerc}}\",\"memory\":\"{{.MemUsage}}\",\"memory_percent\":\"{{.MemPerc}}\",\"network\":\"{{.NetIO}}\",\"block_io\":\"{{.BlockIO}}\",\"pids\":\"{{.PIDs}}\"}" \
      $container_ids >> benchmarks/results/resources.jsonl 2>/dev/null || true
    sleep 1
  done
) &
sampler_pid=$!

status=0
wait "$benchmark_pid" || status=$?
kill "$sampler_pid" 2>/dev/null || true
wait "$sampler_pid" 2>/dev/null || true
exit "$status"
