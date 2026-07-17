#!/usr/bin/env bash
set -euo pipefail

umask 077

if [[ $# -ne 4 ]]; then
  echo "usage: $0 RELEASE_ARCHIVE OUTPUT_DIR FINANCE_DOMAIN SUPABASE_HOST" >&2
  exit 64
fi

release_archive=$1
output_dir=$2
finance_domain=$3
supabase_host=$4
script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)

[[ -f "$release_archive" && ! -L "$release_archive" ]] || {
  echo "release archive is unavailable" >&2
  exit 1
}
[[ "$finance_domain" =~ ^[A-Za-z0-9.-]+$ && "$finance_domain" == *.* ]] || {
  echo "finance domain is invalid" >&2
  exit 1
}
[[ "$supabase_host" =~ ^[A-Za-z0-9.-]+\.supabase\.co$ ]] || {
  echo "Supabase host is invalid" >&2
  exit 1
}
[[ ! -e "$output_dir" ]] || {
  echo "output directory already exists" >&2
  exit 1
}

mkdir -p "$output_dir"
tar -xzf "$release_archive" -C "$output_dir" --no-same-owner --no-same-permissions
mkdir -p "$output_dir/config" "$output_dir/secrets"
install -m 0700 "$script_dir/run.sh" "$output_dir/run.sh"
install -m 0700 "$script_dir/launcher.sh" "$output_dir/nomadhost-root-launcher.sh"
install -m 0600 "$script_dir/nginx.conf.template" "$output_dir/config/nginx.conf.template"
install -m 0600 "$script_dir/security-headers.conf.template" "$output_dir/config/security-headers.conf.template"
install -m 0700 "$script_dir/render-nginx.py" "$output_dir/operations/render-nginx.py"
{
  printf 'FINANCE_DOMAIN=%s\n' "$finance_domain"
  printf 'SUPABASE_HOST=%s\n' "$supabase_host"
  printf 'HSTS_MAX_AGE=0\n'
} > "$output_dir/config/finance.env"
chmod 0600 "$output_dir/config/finance.env"
printf '%s\n' 'Create secrets/database-url on the server with mode 0400. Do not put it in an upload archive.' \
  > "$output_dir/secrets/README.txt"
chmod 0600 "$output_dir/secrets/README.txt"

find "$output_dir" -type d -exec chmod 0700 {} +
find "$output_dir" -type f ! -path "$output_dir/run.sh" ! -path "$output_dir/bin/*" \
  ! -path "$output_dir/operations/*.sh" -exec chmod 0600 {} +
chmod 0700 "$output_dir/bin/finance-api" "$output_dir/bin/goose" "$output_dir/operations/"*.sh

archive_path="${output_dir}.tar.gz"
COPYFILE_DISABLE=1 tar -C "$output_dir" --exclude='._*' --exclude='*/._*' -czf "$archive_path" .

echo "NomadHost bundle prepared at $output_dir"
echo "Upload archive prepared at $archive_path"
