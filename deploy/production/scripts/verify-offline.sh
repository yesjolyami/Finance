#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR=$(cd -- "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)
PRODUCTION_DIR="$ROOT_DIR/deploy/production"
NGINX_TEMPLATE="$PRODUCTION_DIR/templates/nginx-finance.conf"
NGINX_HEADERS="$PRODUCTION_DIR/templates/nginx-security-headers.conf"
SYSTEMD_TEMPLATE="$PRODUCTION_DIR/templates/finance-api.service"
ENV_TEMPLATE="$PRODUCTION_DIR/templates/finance-api.env"

required=(
  "$PRODUCTION_DIR/build-release.sh"
  "$NGINX_TEMPLATE"
  "$NGINX_HEADERS"
  "$SYSTEMD_TEMPLATE"
  "$ENV_TEMPLATE"
  "$PRODUCTION_DIR/scripts/migrate.sh"
  "$PRODUCTION_DIR/scripts/backup.sh"
  "$PRODUCTION_DIR/scripts/restore-drill.sh"
  "$PRODUCTION_DIR/scripts/render-templates.sh"
  "$PRODUCTION_DIR/scripts/smoke.sh"
  "$PRODUCTION_DIR/scripts/prepare-output.py"
  "$PRODUCTION_DIR/scripts/validate-operational-file.py"
  "$PRODUCTION_DIR/scripts/read-auth-token.py"
)
for path in "${required[@]}"; do
  [[ -f "$path" ]] || { echo "production artifact is missing" >&2; exit 1; }
done

sh -n "$PRODUCTION_DIR/build-release.sh"
for script in "$PRODUCTION_DIR"/scripts/*.sh; do
  bash -n "$script"
done
python3 - "$PRODUCTION_DIR/scripts" <<'PY'
import pathlib
import sys

for path in pathlib.Path(sys.argv[1]).glob("*.py"):
    compile(path.read_text(encoding="utf-8"), str(path), "exec")
PY
if rg -n 'rm -rf[^#\n]*OUTPUT_DIR' "$PRODUCTION_DIR/build-release.sh" "$PRODUCTION_DIR/scripts/render-templates.sh"; then
  echo "production output scripts contain destructive cleanup" >&2
  exit 1
fi

grep -Fq 'HTTP_HOST=127.0.0.1' "$ENV_TEMPLATE"
grep -Fq 'DATABASE_URL_FILE=/etc/finance/secrets/database-url' "$ENV_TEMPLATE"
grep -Fq 'API_REPLICA_COUNT=1' "$ENV_TEMPLATE"
grep -Fq 'PRODUCTION_SECURITY_PROFILE=single-proxy-single-replica-v1' "$ENV_TEMPLATE"
grep -Fq 'IMPORT_BACKUP_V5_ENABLED=false' "$ENV_TEMPLATE"
if grep -Eq '^DATABASE_URL=' "$ENV_TEMPLATE"; then
  echo "raw production DATABASE_URL is forbidden" >&2
  exit 1
fi

grep -Fq 'User=finance' "$SYSTEMD_TEMPLATE"
grep -Fq 'ProtectSystem=strict' "$SYSTEMD_TEMPLATE"
grep -Fq 'ProtectHome=true' "$SYSTEMD_TEMPLATE"
grep -Fq 'NoNewPrivileges=true' "$SYSTEMD_TEMPLATE"
grep -Fq 'TimeoutStopSec=90s' "$SYSTEMD_TEMPLATE"
grep -Fq 'UMask=0077' "$SYSTEMD_TEMPLATE"
grep -Fq 'ReadOnlyPaths=/opt/finance /etc/finance' "$SYSTEMD_TEMPLATE"

grep -Fq 'listen 80 default_server;' "$NGINX_TEMPLATE"
grep -Fq 'listen 443 ssl default_server;' "$NGINX_TEMPLATE"
grep -Fq 'ssl_reject_handshake on;' "$NGINX_TEMPLATE"
grep -Fq 'return 308 https://__FINANCE_DOMAIN__$request_uri;' "$NGINX_TEMPLATE"
grep -Fq 'location ^~ /.well-known/acme-challenge/' "$NGINX_TEMPLATE"
if grep -Eq 'return 30[178][^;]*\$host' "$NGINX_TEMPLATE"; then
  echo "nginx redirect reflects the request Host" >&2
  exit 1
fi
grep -Fq 'ssl_protocols TLSv1.2 TLSv1.3;' "$NGINX_TEMPLATE"
grep -Fq 'proxy_pass http://127.0.0.1:8080;' "$NGINX_TEMPLATE"
grep -Fq 'client_max_body_size 32m;' "$NGINX_TEMPLATE"
grep -Fq 'proxy_request_buffering off;' "$NGINX_TEMPLATE"
grep -Fq 'proxy_read_timeout 130s;' "$NGINX_TEMPLATE"
grep -Fq 'proxy_send_timeout 130s;' "$NGINX_TEMPLATE"
grep -Fq 'limit_req zone=finance_import' "$NGINX_TEMPLATE"
grep -Fq 'limit_req_status 429;' "$NGINX_TEMPLATE"
grep -Fq 'proxy_set_header X-Forwarded-For "";' "$NGINX_TEMPLATE"
python3 - "$NGINX_TEMPLATE" <<'PY'
import pathlib
import re
import sys

source = pathlib.Path(sys.argv[1]).read_text(encoding="utf-8")
match = re.search(
    r"location ~ \^/api/v1/households/\[\^/\]\+/imports/backup-v5/\(preview\|confirm\)\$ \{(.*?)\n    \}",
    source,
    re.S,
)
if match is None:
    raise SystemExit("backup import nginx location is missing")
block = match.group(1)
if "include /etc/nginx/snippets/finance-security-headers.conf;" not in block:
    raise SystemExit("backup import security headers are missing")
if 'add_header Cache-Control "no-store" always;' not in block:
    raise SystemExit("backup import no-store policy is missing")
PY
grep -Fq "script-src 'self'" "$NGINX_HEADERS"
grep -Fq "style-src 'self'" "$NGINX_HEADERS"
grep -Fq "frame-ancestors 'none'" "$NGINX_HEADERS"
if grep -Eq "'unsafe-inline'|'unsafe-eval'" "$NGINX_HEADERS"; then
  echo "unsafe CSP directive detected" >&2
  exit 1
fi

if rg -n 'style=\{\{|dangerouslySetInnerHTML|\beval\s*\(|new Function' "$ROOT_DIR/frontend/src" \
  --glob '*.tsx' --glob '*.ts' --glob '!*.test.ts' --glob '!*.test.tsx'; then
  echo "frontend source is incompatible with strict CSP" >&2
  exit 1
fi
if [[ -f "$ROOT_DIR/frontend/dist/index.html" ]]; then
  python3 - "$ROOT_DIR/frontend/dist" <<'PY'
import pathlib
import re
import sys

root = pathlib.Path(sys.argv[1])
html = (root / "index.html").read_text(encoding="utf-8")
if re.search(r"<style\b|\sstyle\s*=", html, re.I):
    raise SystemExit("built frontend contains inline style")
scripts = re.findall(r"<script\b([^>]*)>(.*?)</script>", html, re.I | re.S)
if not scripts or any("src=" not in attrs.lower() or body.strip() for attrs, body in scripts):
    raise SystemExit("built frontend contains an inline or source-less script")
assets = [path.name for path in (root / "assets").iterdir() if path.is_file()]
if not assets or any(re.search(r"-[A-Za-z0-9_-]{8,}\.(?:js|css)$", name) is None for name in assets):
    raise SystemExit("built frontend assets are not content-hashed")
PY
fi

grep -Fq '__FINANCE_DOMAIN__' "$NGINX_TEMPLATE"
grep -Fq '__SUPABASE_HOST__' "$NGINX_HEADERS"
grep -Fq '__HSTS_MAX_AGE__' "$NGINX_HEADERS"
grep -Fq '__FINANCE_DOMAIN__' "$ENV_TEMPLATE"
grep -Fq '__SUPABASE_HOST__' "$ENV_TEMPLATE"

if rg -n --hidden -S 'sb_secret_[A-Za-z0-9_-]{20,}|BEGIN (RSA |EC |OPENSSH )?PRIVATE KEY|postgres(?:ql)?://[^[:space:]]+:[^[:space:]@]+@' \
  "$PRODUCTION_DIR" --glob '!verify-offline.sh' --glob '!build-release.sh'; then
  echo "production templates contain a forbidden secret pattern" >&2
  exit 1
fi

fixture_base=$(
  python3 - <<'PY'
import os
import tempfile

print(os.path.realpath(tempfile.gettempdir()))
PY
)
fixture=$(mktemp -d "$fixture_base/finance-production-verify.XXXXXX")
trap 'rm -rf -- "$fixture"' EXIT
sed \
  -e 's/__FINANCE_DOMAIN__/finance.invalid/g' \
  "$NGINX_TEMPLATE" > "$fixture/nginx.conf"
sed \
  -e 's/__SUPABASE_HOST__/fixture.supabase.co/g' \
  -e 's/__HSTS_MAX_AGE__/300/g' \
  "$NGINX_HEADERS" > "$fixture/security-headers.conf"
if rg -n '__[A-Z0-9_]+__' "$fixture/nginx.conf"; then
  echo "rendered nginx fixture contains unresolved placeholders" >&2
  exit 1
fi

PREPARE_OUTPUT="$PRODUCTION_DIR/scripts/prepare-output.py"
for unsafe in \
  / \
  "$HOME" \
  "$HOME/finance-output" \
  "$ROOT_DIR" \
  "$ROOT_DIR/finance-output" \
  /etc \
  /etc/finance-output \
  /opt/finance-output \
  /private/etc/finance-output \
  /private/var/db/finance-output \
  /private/tmp; do
  if python3 "$PREPARE_OUTPUT" "$unsafe" "$ROOT_DIR" >/dev/null 2>&1; then
    echo "unsafe output directory was accepted" >&2
    exit 1
  fi
done
mkdir "$fixture/symlink-target"
ln -s "$fixture/symlink-target" "$fixture/symlink-output"
if python3 "$PREPARE_OUTPUT" "$fixture/symlink-output" "$ROOT_DIR" >/dev/null 2>&1; then
  echo "symlink output directory was accepted" >&2
  exit 1
fi
if python3 "$PREPARE_OUTPUT" "$fixture/symlink-output/nested" "$ROOT_DIR" >/dev/null 2>&1; then
  echo "output below a symlink component was accepted" >&2
  exit 1
fi

unsafe_empty="$fixture/unsafe-empty-output"
safe_empty="$fixture/safe-empty-output"
printf 'keep-neighbor\n' > "$fixture/output-neighbor-sentinel"
mkdir "$unsafe_empty"
chmod 0777 "$unsafe_empty"
if python3 "$PREPARE_OUTPUT" "$unsafe_empty" "$ROOT_DIR" >/dev/null 2>&1; then
  echo "group/world-writable empty output directory was accepted" >&2
  exit 1
fi
[[ -d "$unsafe_empty" ]]
[[ -z "$(find "$unsafe_empty" -mindepth 1 -maxdepth 1 -print -quit)" ]]
grep -qx 'keep-neighbor' "$fixture/output-neighbor-sentinel"

mkdir "$safe_empty"
chmod 0700 "$safe_empty"
python3 "$PREPARE_OUTPUT" "$safe_empty" "$ROOT_DIR"
[[ -d "$safe_empty" ]]
[[ -z "$(find "$safe_empty" -mindepth 1 -maxdepth 1 -print -quit)" ]]
grep -qx 'keep-neighbor' "$fixture/output-neighbor-sentinel"

sentinel_render="$fixture/nonempty-render"
mkdir "$sentinel_render"
printf 'keep-render\n' > "$sentinel_render/sentinel"
if OUTPUT_DIR="$sentinel_render" \
  FINANCE_DOMAIN=finance-stage5e2-verification.ru \
  SUPABASE_HOST=stage5e2verification.supabase.co \
  HSTS_MAX_AGE=300 \
  "$PRODUCTION_DIR/scripts/render-templates.sh" >/dev/null 2>&1; then
  echo "render accepted a nonempty output directory" >&2
  exit 1
fi
grep -qx 'keep-render' "$sentinel_render/sentinel"

fakebin="$fixture/fakebin"
mkdir "$fakebin"
printf '#!/bin/sh\nprintf "go version go1.26.5 linux/amd64\\n"\n' > "$fakebin/go"
printf '#!/bin/sh\nprintf "v24.14.0\\n"\n' > "$fakebin/node"
printf '#!/bin/sh\nprintf "11.9.0\\n"\n' > "$fakebin/npm"
chmod 0700 "$fakebin/go" "$fakebin/node" "$fakebin/npm"
sentinel_build="$fixture/nonempty-build"
mkdir "$sentinel_build"
printf 'keep-build\n' > "$sentinel_build/sentinel"
if PATH="$fakebin:$PATH" \
  RELEASE_VERSION=0.5.2 \
  SOURCE_DATE_EPOCH=1784235600 \
  OUTPUT_DIR="$sentinel_build" \
  VITE_SUPABASE_URL=https://stage5e2verification.supabase.co \
  VITE_SUPABASE_PUBLISHABLE_KEY=sb_publishable_stage5e2verification000000000000 \
  "$PRODUCTION_DIR/build-release.sh" >/dev/null 2>&1; then
  echo "build accepted a nonempty output directory" >&2
  exit 1
fi
grep -qx 'keep-build' "$sentinel_build/sentinel"

VALIDATE_FILE="$PRODUCTION_DIR/scripts/validate-operational-file.py"
secret_file="$fixture/owner-secret"
printf 'secret\n' > "$secret_file"
chmod 0400 "$secret_file"
python3 "$VALIDATE_FILE" "$secret_file" owner-secret 1024
chmod 0644 "$secret_file"
if python3 "$VALIDATE_FILE" "$secret_file" owner-secret 1024 >/dev/null 2>&1; then
  echo "overly readable owner secret was accepted" >&2
  exit 1
fi
readonly_file="$fixture/readonly"
printf 'public\n' > "$readonly_file"
chmod 0444 "$readonly_file"
python3 "$VALIDATE_FILE" "$readonly_file" readonly 1024
chmod 0664 "$readonly_file"
if python3 "$VALIDATE_FILE" "$readonly_file" readonly 1024 >/dev/null 2>&1; then
  echo "group-writable operational file was accepted" >&2
  exit 1
fi

READ_TOKEN="$PRODUCTION_DIR/scripts/read-auth-token.py"
token_file="$fixture/auth-token"
printf 'aaa.bbb.ccc\n' > "$token_file"
chmod 0400 "$token_file"
[[ "$(python3 "$READ_TOKEN" "$token_file")" == "aaa.bbb.ccc" ]]
chmod 0644 "$token_file"
if python3 "$READ_TOKEN" "$token_file" >/dev/null 2>&1; then
  echo "auth token with unsafe mode was accepted" >&2
  exit 1
fi
chmod 0400 "$token_file"
ln -s "$token_file" "$fixture/auth-token-link"
if python3 "$READ_TOKEN" "$fixture/auth-token-link" >/dev/null 2>&1; then
  echo "symlink auth token was accepted" >&2
  exit 1
fi
injection_file="$fixture/auth-token-injection"
printf 'aaa.bbb.ccc"\nurl = "https://cross-origin.invalid"\n' > "$injection_file"
chmod 0400 "$injection_file"
if python3 "$READ_TOKEN" "$injection_file" >/dev/null 2>&1; then
  echo "curl-config token injection was accepted" >&2
  exit 1
fi

rendered="$fixture/rendered"
OUTPUT_DIR="$rendered" \
  FINANCE_DOMAIN=finance-stage5e2-verification.ru \
  SUPABASE_HOST=stage5e2verification.supabase.co \
  HSTS_MAX_AGE=300 \
  "$PRODUCTION_DIR/scripts/render-templates.sh" >/dev/null
if rg -n '__[A-Z0-9_]+__|^DATABASE_URL=' "$rendered"; then
  echo "rendered production fixture is unsafe" >&2
  exit 1
fi
if OUTPUT_DIR="$fixture/rejected" \
  FINANCE_DOMAIN=finance.invalid \
  SUPABASE_HOST=fixture.supabase.co \
  HSTS_MAX_AGE=300 \
  "$PRODUCTION_DIR/scripts/render-templates.sh" >/dev/null 2>&1; then
  echo "placeholder production fixture was accepted" >&2
  exit 1
fi

grep -Fq 'ack-isolated-empty-restore-database' "$PRODUCTION_DIR/scripts/restore-drill.sh"
grep -Fq 'RESTORE_EXPECTED_DATABASE' "$PRODUCTION_DIR/scripts/restore-drill.sh"
grep -Fq 'SELECT current_database()' "$PRODUCTION_DIR/scripts/restore-drill.sh"
grep -Fq "n.nspname NOT IN ('pg_catalog', 'information_schema')" "$PRODUCTION_DIR/scripts/restore-drill.sh"
grep -Fq 'if [[ "$version" != "4" ]]' "$PRODUCTION_DIR/scripts/restore-drill.sh"
python3 - "$PRODUCTION_DIR/scripts/restore-drill.sh" <<'PY'
import pathlib
import sys

source = pathlib.Path(sys.argv[1]).read_text(encoding="utf-8")
preflight = source.index("SELECT current_database()")
empty_check = source.index("user_table_count=")
restore = source.index("pg_restore --exit-on-error")
if not preflight < empty_check < restore:
    raise SystemExit("restore target checks do not precede pg_restore")
PY

if command -v systemd-analyze >/dev/null 2>&1; then
  rendered_unit="$fixture/finance-api.service"
  sed 's#ExecStart=/opt/finance/current/bin/finance-api#ExecStart=/bin/true#' \
    "$SYSTEMD_TEMPLATE" > "$rendered_unit"
  systemd-analyze verify "$rendered_unit" >/dev/null
fi

echo "offline production templates verification passed"
