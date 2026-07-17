#!/usr/bin/env bash
set -euo pipefail

umask 077

ROOT_DIR=$(cd -- "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)
TEMPLATE_DIR="$ROOT_DIR/deploy/production/templates"
OUTPUT_DIR=${OUTPUT_DIR:-}
FINANCE_DOMAIN=${FINANCE_DOMAIN:-}
SUPABASE_HOST=${SUPABASE_HOST:-}
HSTS_MAX_AGE=${HSTS_MAX_AGE:-}
IMPORT_ACTIVE_KEY_ID=${IMPORT_ACTIVE_KEY_ID:-disabled}
export FINANCE_DOMAIN SUPABASE_HOST HSTS_MAX_AGE IMPORT_ACTIVE_KEY_ID

if [[ -z "$OUTPUT_DIR" || "$OUTPUT_DIR" != /* ]]; then
  echo "OUTPUT_DIR must be an absolute path" >&2
  exit 1
fi
python3 - <<'PY'
import os
import re

domain = os.environ["FINANCE_DOMAIN"]
supabase = os.environ["SUPABASE_HOST"]
for label, value in (("FINANCE_DOMAIN", domain), ("SUPABASE_HOST", supabase)):
    if not re.fullmatch(r"(?=.{1,253}\Z)(?:[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,63}", value):
        raise SystemExit(f"{label} must be an exact lowercase DNS hostname")
    if re.search(r"example|invalid|placeholder|change.?me|your.?project|fixture", value):
        raise SystemExit(f"{label} contains a placeholder")
if not supabase.endswith(".supabase.co"):
    raise SystemExit("SUPABASE_HOST must be the exact hosted Supabase project hostname")
if os.environ["HSTS_MAX_AGE"] not in ("300", "31536000"):
    raise SystemExit("HSTS_MAX_AGE must be 300 for verified canary or 31536000 after observation")
key_id = os.environ["IMPORT_ACTIVE_KEY_ID"]
if not re.fullmatch(r"[A-Za-z0-9][A-Za-z0-9._-]{0,63}", key_id):
    raise SystemExit("IMPORT_ACTIVE_KEY_ID is invalid")
PY

python3 "$ROOT_DIR/deploy/production/scripts/prepare-output.py" "$OUTPUT_DIR" "$ROOT_DIR"
sed \
  -e "s/__FINANCE_DOMAIN__/$FINANCE_DOMAIN/g" \
  -e "s/__SUPABASE_HOST__/$SUPABASE_HOST/g" \
  -e "s/__IMPORT_ACTIVE_KEY_ID__/$IMPORT_ACTIVE_KEY_ID/g" \
  "$TEMPLATE_DIR/finance-api.env" > "$OUTPUT_DIR/finance-api.env"
sed \
  -e "s/__FINANCE_DOMAIN__/$FINANCE_DOMAIN/g" \
  "$TEMPLATE_DIR/nginx-finance.conf" > "$OUTPUT_DIR/nginx-finance.conf"
sed \
  -e "s/__SUPABASE_HOST__/$SUPABASE_HOST/g" \
  -e "s/__HSTS_MAX_AGE__/$HSTS_MAX_AGE/g" \
  "$TEMPLATE_DIR/nginx-security-headers.conf" > "$OUTPUT_DIR/finance-security-headers.conf"
cp "$TEMPLATE_DIR/finance-api.service" "$OUTPUT_DIR/"

if rg -n '__[A-Z0-9_]+__' "$OUTPUT_DIR"; then
  echo "rendered templates contain unresolved placeholders" >&2
  exit 1
fi
chmod 0600 "$OUTPUT_DIR/finance-api.env"
chmod 0644 "$OUTPUT_DIR/nginx-finance.conf" "$OUTPUT_DIR/finance-security-headers.conf" "$OUTPUT_DIR/finance-api.service"
echo "non-secret production templates rendered"
