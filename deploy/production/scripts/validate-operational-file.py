#!/usr/bin/env python3

import os
import stat
import sys


def fail() -> None:
    raise SystemExit("operational file is invalid")


try:
    if len(sys.argv) != 4:
        fail()
    path, policy, raw_limit = sys.argv[1:]
    limit = int(raw_limit)
    if limit < 1 or limit > 1024 * 1024:
        fail()
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
    if not stat.S_ISREG(info.st_mode) or info.st_size < 1 or info.st_size > limit:
        os.close(descriptor)
        fail()
    if policy == "owner-secret":
        if info.st_uid != os.geteuid() or mode not in (0o400, 0o600):
            os.close(descriptor)
            fail()
    elif policy == "readonly":
        if mode & 0o022:
            os.close(descriptor)
            fail()
    else:
        os.close(descriptor)
        fail()
    os.close(descriptor)
except SystemExit:
    raise
except Exception:
    fail()
