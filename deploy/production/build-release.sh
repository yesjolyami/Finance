#!/bin/sh
set -eu

umask 077

ROOT_DIR=$(CDPATH= cd -- "$(dirname "$0")/../.." && pwd)
RELEASE_VERSION=${RELEASE_VERSION:-}
OUTPUT_DIR=${OUTPUT_DIR:-}
SOURCE_DATE_EPOCH=${SOURCE_DATE_EPOCH:-}
TARGET_ARCH=${TARGET_ARCH:-amd64}
EXPECTED_GO_VERSION=go1.26.5
EXPECTED_NODE_VERSION=v24.14.0
EXPECTED_NPM_VERSION=11.9.0

case "$RELEASE_VERSION" in
  [0-9]*.[0-9]*.[0-9]*) ;;
  *) echo "RELEASE_VERSION must be an explicit semantic version" >&2; exit 1 ;;
esac
case "$TARGET_ARCH" in
  amd64|arm64) ;;
  *) echo "TARGET_ARCH must be amd64 or arm64" >&2; exit 1 ;;
esac
if [ -z "$OUTPUT_DIR" ] || [ "${OUTPUT_DIR#/}" = "$OUTPUT_DIR" ]; then
  echo "OUTPUT_DIR must be an explicit absolute path" >&2
  exit 1
fi
if [ -z "$SOURCE_DATE_EPOCH" ]; then
  echo "SOURCE_DATE_EPOCH is required for reproducible metadata" >&2
  exit 1
fi
if [ "$(go version | awk '{print $3}')" != "$EXPECTED_GO_VERSION" ] ||
  [ "$(node --version)" != "$EXPECTED_NODE_VERSION" ] ||
  [ "$(npm --version)" != "$EXPECTED_NPM_VERSION" ]; then
  echo "build toolchain version does not match the pinned production contract" >&2
  exit 1
fi

export VITE_API_BASE_URL=""
export VITE_SUPABASE_URL=${VITE_SUPABASE_URL:-}
export VITE_SUPABASE_PUBLISHABLE_KEY=${VITE_SUPABASE_PUBLISHABLE_KEY:-}
export RELEASE_VERSION SOURCE_DATE_EPOCH
export EXPECTED_GO_VERSION EXPECTED_NODE_VERSION EXPECTED_NPM_VERSION

python3 - <<'PY'
import os
import re
import base64
import json
from urllib.parse import urlsplit

version = os.environ["RELEASE_VERSION"]
if re.fullmatch(r"[0-9]+\.[0-9]+\.[0-9]+(?:[-+][0-9A-Za-z.-]+)?", version) is None:
    raise SystemExit("RELEASE_VERSION is not canonical")
url = urlsplit(os.environ["VITE_SUPABASE_URL"])
if url.scheme != "https" or not url.hostname or url.username or url.password or url.query or url.fragment:
    raise SystemExit("VITE_SUPABASE_URL must be an exact HTTPS origin")
if url.path not in ("", "/"):
    raise SystemExit("VITE_SUPABASE_URL must not contain a path")
if re.search(r"placeholder|change.?me|your.?project|example|invalid|fixture", url.hostname, re.I):
    raise SystemExit("VITE_SUPABASE_URL contains a placeholder")
key = os.environ["VITE_SUPABASE_PUBLISHABLE_KEY"]
if len(key) < 24 or re.search(r"placeholder|change.?me|your.?key|service.?role|sb_secret_", key, re.I):
    raise SystemExit("VITE_SUPABASE_PUBLISHABLE_KEY must be a real public anon/publishable key")
if key.startswith("sb_publishable_"):
    pass
elif key.count(".") == 2:
    payload = key.split(".")[1]
    payload += "=" * (-len(payload) % 4)
    try:
        claims = json.loads(base64.urlsafe_b64decode(payload))
    except Exception as error:
        raise SystemExit("legacy Supabase anon key is malformed") from error
    if claims.get("role") != "anon":
        raise SystemExit("legacy Supabase key must have anon role")
else:
    raise SystemExit("VITE_SUPABASE_PUBLISHABLE_KEY format is not supported")
PY

python3 "$ROOT_DIR/deploy/production/scripts/prepare-output.py" "$OUTPUT_DIR" "$ROOT_DIR"
mkdir -p "$OUTPUT_DIR/bin" "$OUTPUT_DIR/frontend" "$OUTPUT_DIR/database" "$OUTPUT_DIR/operations"

if [ "${SKIP_NPM_CI:-false}" != "true" ]; then
  (cd "$ROOT_DIR/frontend" && npm ci --ignore-scripts)
fi
(cd "$ROOT_DIR/frontend" && npm run build)
cp -R "$ROOT_DIR/frontend/dist/." "$OUTPUT_DIR/frontend/"

(cd "$ROOT_DIR/backend" && \
  CGO_ENABLED=0 GOOS=linux GOARCH="$TARGET_ARCH" \
  go build -trimpath -buildvcs=false -ldflags="-s -w -buildid=" \
  -o "$OUTPUT_DIR/bin/finance-api" ./cmd/api)
(cd "$ROOT_DIR/database" && \
  CGO_ENABLED=0 GOOS=linux GOARCH="$TARGET_ARCH" \
  go build -trimpath -buildvcs=false -ldflags="-s -w -buildid=" \
  -o "$OUTPUT_DIR/bin/goose" github.com/pressly/goose/v3/cmd/goose)

cp -R "$ROOT_DIR/database/migrations" "$OUTPUT_DIR/database/"
cp -R "$ROOT_DIR/database/tests" "$OUTPUT_DIR/database/"
cp "$ROOT_DIR/database/go.mod" "$ROOT_DIR/database/go.sum" "$OUTPUT_DIR/database/"
cp "$ROOT_DIR/deploy/production/runtime-dependencies.txt" "$OUTPUT_DIR/"
cp "$ROOT_DIR/deploy/production/scripts/migrate.sh" "$OUTPUT_DIR/operations/"
cp "$ROOT_DIR/deploy/production/scripts/backup.sh" "$OUTPUT_DIR/operations/"
cp "$ROOT_DIR/deploy/production/scripts/restore-drill.sh" "$OUTPUT_DIR/operations/"
cp "$ROOT_DIR/deploy/production/scripts/smoke.sh" "$OUTPUT_DIR/operations/"
cp "$ROOT_DIR/deploy/production/scripts/validate-operational-file.py" "$OUTPUT_DIR/operations/"
cp "$ROOT_DIR/deploy/production/scripts/read-auth-token.py" "$OUTPUT_DIR/operations/"
cp "$ROOT_DIR/deploy/production/templates/finance-api.service" "$OUTPUT_DIR/operations/"
cp "$ROOT_DIR/deploy/production/templates/finance-api.env" "$OUTPUT_DIR/operations/"
cp "$ROOT_DIR/deploy/production/templates/nginx-finance.conf" "$OUTPUT_DIR/operations/"
cp "$ROOT_DIR/deploy/production/templates/nginx-security-headers.conf" "$OUTPUT_DIR/operations/"
cp "$ROOT_DIR/deploy/production/templates/50x.html" "$OUTPUT_DIR/frontend/"
cp "$ROOT_DIR/docs/production-operations-stage5.md" "$OUTPUT_DIR/operations/production-runbook.md"

python3 - "$OUTPUT_DIR" <<'PY'
import os
import pathlib
import sys

root = pathlib.Path(sys.argv[1])
timestamp = int(os.environ["SOURCE_DATE_EPOCH"])
for path in sorted(root.rglob("*"), reverse=True):
    os.utime(path, (timestamp, timestamp), follow_symlinks=False)
os.utime(root, (timestamp, timestamp), follow_symlinks=False)
PY

if command -v sha256sum >/dev/null 2>&1; then
  HASH_COMMAND=sha256sum
else
  HASH_COMMAND="shasum -a 256"
fi
(
  cd "$OUTPUT_DIR"
  find . -type f ! -name SHA256SUMS ! -name manifest.json -print | LC_ALL=C sort |
    while IFS= read -r file; do
      $HASH_COMMAND "$file"
    done > SHA256SUMS
)

python3 - "$OUTPUT_DIR" "$TARGET_ARCH" <<'PY'
import hashlib
import json
import os
import pathlib
import sys

output = pathlib.Path(sys.argv[1])
files = {}
for path in sorted(output.rglob("*")):
    if path.is_file() and path.name != "manifest.json":
        files[str(path.relative_to(output))] = hashlib.sha256(path.read_bytes()).hexdigest()
manifest = {
    "artifactVersion": 1,
    "releaseVersion": os.environ["RELEASE_VERSION"],
    "sourceDateEpoch": int(os.environ["SOURCE_DATE_EPOCH"]),
    "target": {"os": "linux", "arch": sys.argv[2], "cgo": False},
    "buildToolchain": {
        "go": os.environ["EXPECTED_GO_VERSION"],
        "node": os.environ["EXPECTED_NODE_VERSION"],
        "npm": os.environ["EXPECTED_NPM_VERSION"],
    },
    "databaseMigrationVersion": 4,
    "files": files,
}
(output / "manifest.json").write_text(
    json.dumps(manifest, ensure_ascii=True, indent=2, sort_keys=True) + "\n",
    encoding="utf-8",
)
PY

python3 - "$OUTPUT_DIR" <<'PY'
import os
import pathlib
import sys

root = pathlib.Path(sys.argv[1])
timestamp = int(os.environ["SOURCE_DATE_EPOCH"])
for path in sorted(root.rglob("*"), reverse=True):
    os.utime(path, (timestamp, timestamp), follow_symlinks=False)
os.utime(root, (timestamp, timestamp), follow_symlinks=False)
PY

if rg -n --hidden -S 'sb_secret_[A-Za-z0-9_-]{20,}|BEGIN (RSA |EC |OPENSSH )?PRIVATE KEY|postgres(?:ql)?://[^[:space:]]+:[^[:space:]@]+@' "$OUTPUT_DIR"; then
  echo "release artifact contains a forbidden secret pattern" >&2
  exit 1
fi

echo "Release artifacts created in $OUTPUT_DIR"
