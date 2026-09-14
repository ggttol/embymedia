#!/bin/sh
set -eu

config=/etc/embymedia-nas-worker.conf
test -r "$config"
. "$config"
read -r destination
exec python3 -c '
import os, sys
root, uid, gid, relative = sys.argv[1], int(sys.argv[2]), int(sys.argv[3]), sys.argv[4]
parts = relative.split("/")
if not relative or len(relative) > 4096 or len(parts) > 32 or any(part in ("", ".", "..") for part in parts):
    raise SystemExit("invalid NAS worker destination")
flags = os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW | os.O_CLOEXEC
descriptor = os.open(root, flags)
try:
    for part in parts:
        child = os.open(part, flags, dir_fd=descriptor)
        os.close(descriptor)
        descriptor = child
    os.fchown(descriptor, uid, gid)
    os.fchmod(descriptor, 0o700)
finally:
    os.close(descriptor)
' "$NAS_WORKER_MOUNT_ROOT" "$NAS_WORKER_UID" "$NAS_WORKER_GID" "$destination"
