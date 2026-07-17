#!/usr/bin/env bash
set -euo pipefail

umask 077

DOMAIN=${DOMAIN:-}
AUTH_TOKEN_FILE=${AUTH_TOKEN_FILE:-}
DIRECT_API_URL=${DIRECT_API_URL:-}

if [[ -z "$DOMAIN" || "$DOMAIN" == *"/"* || "$DOMAIN" == *"__"* ]]; then
  echo "DOMAIN must be an exact deployed hostname" >&2
  exit 1
fi

temporary=$(mktemp -d)
trap 'rm -rf -- "$temporary"' EXIT
headers="$temporary/headers"

status=$(curl --silent --show-error --dump-header "$headers" --output /dev/null --write-out '%{http_code}' "http://$DOMAIN/")
[[ "$status" == "308" ]]
grep -Fq "Location: https://$DOMAIN/" "$headers"

curl --silent --show-error --fail --dump-header "$headers" --output /dev/null "https://$DOMAIN/"
grep -Eiq '^Strict-Transport-Security: max-age=[1-9][0-9]*' "$headers"
grep -Eiq "^Content-Security-Policy: .*script-src 'self'.*style-src 'self'" "$headers"
if grep -Eiq "^Content-Security-Policy: .*'unsafe-(inline|eval)'" "$headers"; then
  echo "unsafe CSP detected" >&2
  exit 1
fi
grep -Eiq '^X-Content-Type-Options: nosniff' "$headers"
grep -Eiq '^Cache-Control: no-store' "$headers"

curl --silent --show-error --fail --output /dev/null "https://$DOMAIN/api/health"

status=$(curl --silent --show-error --output /dev/null --write-out '%{http_code}' \
  --request POST --header 'Content-Type: application/json' \
  --header 'Origin: https://cross-origin.invalid' --data '{}' \
  "https://$DOMAIN/api/v1/session/bootstrap")
[[ "$status" == "403" ]]
status=$(curl --silent --show-error --output /dev/null --write-out '%{http_code}' \
  --request POST --header 'Content-Type: application/json' --data '{}' \
  "https://$DOMAIN/api/v1/session/bootstrap")
[[ "$status" == "403" ]]

if [[ -n "$DIRECT_API_URL" ]] && curl --silent --show-error --max-time 3 --output /dev/null "$DIRECT_API_URL/api/health"; then
  echo "direct Go listener is externally reachable" >&2
  exit 1
fi

if [[ -n "$AUTH_TOKEN_FILE" ]]; then
  READ_TOKEN=$(cd -- "$(dirname "${BASH_SOURCE[0]}")" && pwd)/read-auth-token.py
  token=$(python3 "$READ_TOKEN" "$AUTH_TOKEN_FILE") ||
    { echo "AUTH_TOKEN_FILE is unavailable" >&2; exit 1; }
  config="$temporary/curl-auth.conf"
  {
    printf 'silent\nshow-error\nheader = "Authorization: Bearer %s"\n' "$token"
  } > "$config"
  chmod 0600 "$config"
  unset token
  status=$(curl --config "$config" --output /dev/null --write-out '%{http_code}' \
    --request POST --header "Origin: https://$DOMAIN" \
    --header 'Content-Type: application/json' \
    --header 'Content-Encoding: gzip' \
    --header 'Import-Budget-Month: 2026-01-01' \
    --data-binary '{}' \
    "https://$DOMAIN/api/v1/households/00000000-0000-0000-0000-000000000000/imports/backup-v5/preview")
  [[ "$status" == "400" ]]
fi

echo "production smoke checks passed"
