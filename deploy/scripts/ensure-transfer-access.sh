#!/bin/sh
set -eu

# The CloudDrive2 FUSE mount reports every directory as root-owned 0755 and does
# not propagate POSIX default ACLs to subdirectories it creates on demand. The
# EmbyMedia service therefore cannot write into a newly appeared transfer
# destination, such as a 115 folder created through the provider API, until an
# operator grants access. This helper grants the service user explicit read/write
# access on the fixed transfer destinations, and the CloudDrive monitor re-runs it
# so directories created later become writable without a service restart.

mount_path=${EMBYMEDIA_CLOUDDRIVE_MOUNT:-/srv/embymedia/data/clouddrive/CloudNAS/CloudDrive}
service_user=embymedia
trees='_待整理 电视剧追更 综艺追更'

[ "$(id -u)" -eq 0 ] || { echo 'run as root' >&2; exit 1; }
command -v setfacl >/dev/null || { echo 'install the acl package before granting transfer access' >&2; exit 1; }
id -u "$service_user" >/dev/null 2>&1 || { echo "transfer access requires the $service_user account" >&2; exit 1; }
if ! findmnt --mountpoint "$mount_path" >/dev/null 2>&1; then
  echo 'CloudDrive2 mount is not present; transfer access left unchanged' >&2
  exit 0
fi

granted=0
for tree in $trees; do
  target="$mount_path/$tree"
  [ -d "$target" ] || continue
  granted=$((granted + 1))
  find "$target" -xdev -type d | while IFS= read -r directory; do
    # Skip directories that already carry both the access and the inheritable
    # default entry so the periodic refresh does not rewrite them.
    entries=$(getfacl -cp "$directory" 2>/dev/null | grep -cE "^(default:)?user:$service_user:rwx$" || true)
    [ "$entries" -ge 2 ] && continue
    setfacl -m "u:$service_user:rwx,d:u:$service_user:rwx" "$directory"
    echo "granted transfer access on $directory" >&2
  done
done

if [ "$granted" -eq 0 ]; then
  echo 'no transfer destination exists yet; transfer access left unchanged' >&2
fi
exit 0
