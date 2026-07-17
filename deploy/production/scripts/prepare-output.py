#!/usr/bin/env python3

import os
import pathlib
import stat
import sys
import tempfile


def fail() -> None:
    raise SystemExit("output directory is unsafe or not empty")


if len(sys.argv) != 3:
    fail()

raw_output = sys.argv[1]
raw_repository = sys.argv[2]
if not raw_output.startswith("/") or os.path.normpath(raw_output) != raw_output:
    fail()

output = pathlib.Path(raw_output)
repository = pathlib.Path(raw_repository).resolve(strict=True)
home = pathlib.Path.home().resolve(strict=True)
temporary_root = pathlib.Path(os.path.realpath(tempfile.gettempdir()))
forbidden_trees = {
    pathlib.Path("/Applications"),
    pathlib.Path("/Library"),
    pathlib.Path("/System"),
    pathlib.Path("/Users"),
    pathlib.Path("/bin"),
    pathlib.Path("/boot"),
    pathlib.Path("/dev"),
    pathlib.Path("/etc"),
    pathlib.Path("/home"),
    pathlib.Path("/lib"),
    pathlib.Path("/lib64"),
    pathlib.Path("/opt"),
    pathlib.Path("/private/etc"),
    pathlib.Path("/private/home"),
    pathlib.Path("/private/opt"),
    pathlib.Path("/private/root"),
    pathlib.Path("/private/usr"),
    pathlib.Path("/proc"),
    pathlib.Path("/root"),
    pathlib.Path("/run"),
    pathlib.Path("/sbin"),
    pathlib.Path("/srv"),
    pathlib.Path("/sys"),
    pathlib.Path("/usr"),
    pathlib.Path("/var"),
}
forbidden_exact = {
    pathlib.Path("/"),
    pathlib.Path("/private"),
    pathlib.Path("/private/tmp"),
    pathlib.Path("/tmp"),
    home,
}
if (
    output in forbidden_exact
    or output == home
    or home in output.parents
    or output == repository
    or repository in output.parents
    or any(output == forbidden or forbidden in output.parents for forbidden in forbidden_trees)
    or (
        pathlib.Path("/private/var") in output.parents
        and output != temporary_root
        and temporary_root not in output.parents
    )
):
    fail()

current = pathlib.Path("/")
for part in output.parts[1:]:
    current /= part
    if os.path.lexists(current):
        info = os.lstat(current)
        if stat.S_ISLNK(info.st_mode):
            fail()

if os.path.lexists(output):
    info = os.lstat(output)
    mode = stat.S_IMODE(info.st_mode)
    if (
        not stat.S_ISDIR(info.st_mode)
        or info.st_uid != os.geteuid()
        or mode & 0o022 != 0
    ):
        fail()
    with os.scandir(output) as entries:
        try:
            next(entries)
        except StopIteration:
            pass
        else:
            fail()
else:
    parent = output.parent
    if not parent.is_dir():
        fail()
    os.mkdir(output, 0o700)
