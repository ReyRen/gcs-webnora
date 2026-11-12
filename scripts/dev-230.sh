#!/bin/sh
# Source is mounted read-only. Dependencies and generated files live in /runtime.
set -eu

case "${1:-}" in
  app)
    mkdir -p /runtime/bin /runtime/tmp /runtime/logs
    if [ ! -x /runtime/bin/air ]; then
      GOBIN=/runtime/bin go install github.com/air-verse/air@v1.67.4
    fi
    exec /runtime/bin/air -c /workspace/deploy/dev-230.air.toml
    ;;
  frontend)
    cd /workspace/frontend
    if [ ! -x node_modules/.bin/vite ]; then
      npm ci --no-audit --no-fund
    fi
    exec npm run dev -- --host 127.0.0.1 --port 5174 --strictPort
    ;;
  *)
    echo "Usage: $0 app|frontend" >&2
    exit 2
    ;;
esac
