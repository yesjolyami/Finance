#!/usr/bin/env bash
set -euo pipefail

umask 077

MIGRATION_DIR=${MIGRATION_DIR:-/opt/finance/current/database/migrations}
DATABASE_TOOL_DIR=${DATABASE_TOOL_DIR:-/opt/finance/current/database}
DATABASE_URL_FILE=${DATABASE_URL_FILE:-/etc/finance/secrets/database-url}
GOOSE_BIN=${GOOSE_BIN:-/opt/finance/current/bin/goose}
EXPECTED_VERSION=5

if [[ ! -d "$MIGRATION_DIR" || ! -x "$GOOSE_BIN" ]]; then
  echo "migration artifacts are unavailable" >&2
  exit 1
fi
if [[ ! -f "$DATABASE_URL_FILE" || -L "$DATABASE_URL_FILE" ]]; then
  echo "database credential is unavailable" >&2
  exit 1
fi

python3 - "$DATABASE_URL_FILE" <<'PY'
import os
import stat
import sys

info = os.lstat(sys.argv[1])
mode = stat.S_IMODE(info.st_mode)
if not stat.S_ISREG(info.st_mode) or info.st_uid != os.geteuid() or mode not in (0o400, 0o600) or info.st_size > 8192:
    raise SystemExit("database credential is invalid")
PY
GOOSE_DBSTRING=$(python3 - "$DATABASE_URL_FILE" <<'PY'
import pathlib
import sys

raw = pathlib.Path(sys.argv[1]).read_bytes()
if raw.endswith(b"\n"):
    raw = raw[:-1]
if not raw or b"\x00" in raw or b"\r" in raw or b"\n" in raw or raw.strip() != raw:
    raise SystemExit("database credential is invalid")
try:
    value = raw.decode("utf-8")
except UnicodeDecodeError as error:
    raise SystemExit("database credential is invalid") from error
sys.stdout.write(value)
PY
)
export GOOSE_DRIVER=postgres GOOSE_DBSTRING

"$GOOSE_BIN" -version | grep -Fq 'v3.24.3'
"$GOOSE_BIN" -dir "$MIGRATION_DIR" status
"$GOOSE_BIN" -dir "$MIGRATION_DIR" up
version=$("$GOOSE_BIN" -dir "$MIGRATION_DIR" version 2>&1 | awk 'END {print $NF}')
if [[ "$version" != "$EXPECTED_VERSION" ]]; then
  echo "migration version verification failed" >&2
  exit 1
fi
unset GOOSE_DBSTRING
echo "database migrations are at expected version"
