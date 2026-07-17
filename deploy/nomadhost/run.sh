#!/usr/bin/env bash
set -euo pipefail

umask 077

ROOT_DIR=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
CONFIG_FILE="$ROOT_DIR/config/finance.env"
DATABASE_URL_FILE="$ROOT_DIR/secrets/database-url"
RUNTIME_DIR="$ROOT_DIR/run"
NGINX_TEMPLATE="$ROOT_DIR/config/nginx.conf.template"
NGINX_CONFIG="$RUNTIME_DIR/nginx.conf"
SECURITY_HEADERS_TEMPLATE="$ROOT_DIR/config/security-headers.conf.template"
SECURITY_HEADERS_CONFIG="$RUNTIME_DIR/security-headers.conf"
NGINX_RENDERER="$ROOT_DIR/operations/render-nginx.py"

if [[ ! -f "$CONFIG_FILE" || -L "$CONFIG_FILE" ]]; then
  echo "config/finance.env is unavailable" >&2
  exit 1
fi
# shellcheck disable=SC1090
source "$CONFIG_FILE"

: "${SERVER_PORT:?SERVER_PORT is required by NomadHost}"
: "${FINANCE_DOMAIN:?FINANCE_DOMAIN is required}"
: "${SUPABASE_HOST:?SUPABASE_HOST is required}"
: "${HSTS_MAX_AGE:=0}"

if [[ ! "$SERVER_PORT" =~ ^[0-9]{1,5}$ ]] || (( SERVER_PORT < 1 || SERVER_PORT > 65535 )); then
  echo "SERVER_PORT is invalid" >&2
  exit 1
fi
if [[ ! "$FINANCE_DOMAIN" =~ ^[A-Za-z0-9.-]+$ ]] || [[ "$FINANCE_DOMAIN" != *.* ]]; then
  echo "FINANCE_DOMAIN is invalid" >&2
  exit 1
fi
if [[ ! "$SUPABASE_HOST" =~ ^[A-Za-z0-9.-]+\.supabase\.co$ ]]; then
  echo "SUPABASE_HOST is invalid" >&2
  exit 1
fi
if [[ ! "$HSTS_MAX_AGE" =~ ^[0-9]+$ ]]; then
  echo "HSTS_MAX_AGE is invalid" >&2
  exit 1
fi
for command in nginx python3; do
  command -v "$command" >/dev/null 2>&1 || {
    echo "$command is required in the Ubuntu image" >&2
    exit 1
  }
done

mkdir -p "$RUNTIME_DIR" "$RUNTIME_DIR/client_temp" "$RUNTIME_DIR/proxy_temp" \
  "$RUNTIME_DIR/fastcgi_temp" "$RUNTIME_DIR/uwsgi_temp" "$RUNTIME_DIR/scgi_temp"
chmod 0700 "$ROOT_DIR/secrets" "$RUNTIME_DIR"
if [[ ! -f "$DATABASE_URL_FILE" || -L "$DATABASE_URL_FILE" ]]; then
  echo "secrets/database-url is unavailable" >&2
  exit 1
fi
chmod 0400 "$DATABASE_URL_FILE"

DATABASE_URL_FILE="$DATABASE_URL_FILE" \
MIGRATION_DIR="$ROOT_DIR/database/migrations" \
DATABASE_TOOL_DIR="$ROOT_DIR/database" \
GOOSE_BIN="$ROOT_DIR/bin/goose" \
  "$ROOT_DIR/operations/migrate.sh"

python3 "$NGINX_RENDERER" "$NGINX_TEMPLATE" "$NGINX_CONFIG" "$SECURITY_HEADERS_TEMPLATE" \
  "$SECURITY_HEADERS_CONFIG" "$ROOT_DIR" "$RUNTIME_DIR" "$SERVER_PORT" \
  "$FINANCE_DOMAIN" "$SUPABASE_HOST" "$HSTS_MAX_AGE"

nginx -t -c "$NGINX_CONFIG"

cleanup() {
  local status=$?
  trap - EXIT INT TERM
  [[ -n "${api_pid:-}" ]] && kill -TERM "$api_pid" 2>/dev/null || true
  [[ -n "${api_pid:-}" ]] && wait "$api_pid" 2>/dev/null || true
  exit "$status"
}
trap cleanup EXIT INT TERM

env \
  APP_ENV=production \
  HTTP_HOST=127.0.0.1 \
  HTTP_PORT=8080 \
  DATABASE_URL_FILE="$DATABASE_URL_FILE" \
  FRONTEND_ORIGINS="https://$FINANCE_DOMAIN" \
  AUTH_ISSUER="https://$SUPABASE_HOST/auth/v1" \
  AUTH_AUDIENCE=authenticated \
  AUTH_JWKS_URL="https://$SUPABASE_HOST/auth/v1/.well-known/jwks.json" \
  AUTH_JWKS_CACHE_TTL=10m \
  AUTH_JWKS_REFRESH_COOLDOWN=30s \
  AUTH_CLOCK_SKEW=30s \
  AUTH_JWKS_HTTP_TIMEOUT=2s \
  PRODUCTION_SECURITY_PROFILE=single-proxy-single-replica-v1 \
  PRODUCTION_SECURITY_ACK=ack-single-proxy-single-replica-v1 \
  API_REPLICA_COUNT=1 \
  IMPORT_BACKUP_V5_ENABLED=false \
  "$ROOT_DIR/bin/finance-api" &
api_pid=$!

ready=false
for _ in {1..20}; do
  if python3 - <<'PY'
import urllib.request

try:
    with urllib.request.urlopen("http://127.0.0.1:8080/api/health", timeout=1) as response:
        if response.status != 200:
            raise SystemExit(1)
except OSError:
    raise SystemExit(1)
PY
  then
    ready=true
    break
  fi
  if ! kill -0 "$api_pid" 2>/dev/null; then
    wait "$api_pid"
  fi
  sleep 1
done
if [[ "$ready" != true ]]; then
  echo "finance API did not become ready" >&2
  exit 1
fi

nginx -c "$NGINX_CONFIG" -g 'daemon off;'
