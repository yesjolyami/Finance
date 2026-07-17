#!/usr/bin/env python3
from __future__ import annotations

import pathlib
import re
import sys


def nginx_path(value: str) -> str:
    if not value.startswith("/") or any(character in value for character in "{};\"'\\"):
        raise SystemExit("nginx filesystem path is invalid")
    return value


def main(arguments: list[str]) -> int:
    if len(arguments) != 11:
        raise SystemExit(
            "usage: render-nginx.py NGINX_TEMPLATE NGINX_OUTPUT HEADERS_TEMPLATE "
            "HEADERS_OUTPUT ROOT RUNTIME PORT DOMAIN SUPABASE_HOST HSTS_MAX_AGE"
        )
    (
        template_path,
        output_path,
        headers_template_path,
        headers_output_path,
        root,
        runtime,
        port,
        domain,
        supabase,
        hsts,
    ) = arguments[1:]
    values = (root, runtime, port, domain, supabase, hsts)
    if any("\n" in value or "\r" in value for value in values):
        raise SystemExit("nginx render input is invalid")
    if not re.fullmatch(r"[0-9]{1,5}", port) or not 1 <= int(port) <= 65535:
        raise SystemExit("nginx port is invalid")
    if not re.fullmatch(r"[A-Za-z0-9.-]+", domain) or "." not in domain:
        raise SystemExit("nginx domain is invalid")
    if not re.fullmatch(r"[A-Za-z0-9.-]+\.supabase\.co", supabase):
        raise SystemExit("nginx Supabase host is invalid")
    if not re.fullmatch(r"[0-9]+", hsts):
        raise SystemExit("nginx HSTS value is invalid")

    replacements = {
        "__RUNTIME_DIR__": nginx_path(runtime),
        "__SERVER_PORT__": port,
        "__FINANCE_DOMAIN__": domain,
        "__FRONTEND_DIR__": nginx_path(str(pathlib.Path(root) / "frontend")),
        "__SECURITY_HEADERS_FILE__": nginx_path(headers_output_path),
        "__SUPABASE_HOST__": supabase,
        "__HSTS_MAX_AGE__": hsts,
    }
    for source, destination in (
        (template_path, output_path),
        (headers_template_path, headers_output_path),
    ):
        text = pathlib.Path(source).read_text(encoding="utf-8")
        for placeholder, value in replacements.items():
            text = text.replace(placeholder, value)
        if re.search(r"__[A-Z0-9_]+__", text):
            raise SystemExit("nginx template contains unresolved placeholders")
        pathlib.Path(destination).write_text(text, encoding="utf-8")
    return 0


if __name__ == "__main__":
    raise SystemExit(main(sys.argv))
