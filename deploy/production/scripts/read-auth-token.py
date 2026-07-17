#!/usr/bin/env python3

import os
import re
import stat
import sys


def fail() -> None:
    raise SystemExit("AUTH_TOKEN_FILE is invalid")


try:
    if len(sys.argv) != 2:
        fail()
    path = sys.argv[1]
    path_info = os.lstat(path)
    if stat.S_ISLNK(path_info.st_mode):
        fail()
    flags = os.O_RDONLY
    if hasattr(os, "O_CLOEXEC"):
        flags |= os.O_CLOEXEC
    if hasattr(os, "O_NOFOLLOW"):
        flags |= os.O_NOFOLLOW
    descriptor = os.open(path, flags)
    info = os.fstat(descriptor)
    if (path_info.st_dev, path_info.st_ino) != (info.st_dev, info.st_ino):
        os.close(descriptor)
        fail()
    mode = stat.S_IMODE(info.st_mode)
    if (
        not stat.S_ISREG(info.st_mode)
        or info.st_uid != os.geteuid()
        or mode not in (0o400, 0o600)
        or info.st_size < 1
        or info.st_size > 16384
    ):
        os.close(descriptor)
        fail()
    try:
        raw = os.read(descriptor, info.st_size + 1)
    finally:
        os.close(descriptor)
    if len(raw) != info.st_size:
        fail()
    if raw.endswith(b"\n"):
        raw = raw[:-1]
    if (
        not raw
        or b"\r" in raw
        or b"\n" in raw
        or re.fullmatch(rb"[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+", raw) is None
    ):
        fail()
    sys.stdout.write(raw.decode("ascii"))
except SystemExit:
    raise
except Exception:
    fail()
