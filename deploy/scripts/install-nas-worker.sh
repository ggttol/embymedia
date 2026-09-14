#!/bin/sh
set -eu
umask 077

source_tree=${1:?source tree required}
public_key_file=${2:?controller public key required}
worker_home=${EMBYMEDIA_NAS_WORKER_HOME:-/volume1/homes/gaotao/embymedia-transfer}
config_file=$worker_home/config/worker.env
if [ -f "$config_file" ]; then . "$config_file"; fi
spool_dir=${EMBYMEDIA_NAS_WORKER_SPOOL_DIR:-${EMBYMEDIA_WORKER_SPOOL_DIR:-$worker_home/spool}}
mount_root=${EMBYMEDIA_NAS_WORKER_MOUNT_ROOT:-${EMBYMEDIA_WORKER_MOUNT_ROOT:-/volume1/docker/embymedia-clouddrive/CloudNAS/CloudDrive}}
minimum_free=${EMBYMEDIA_NAS_WORKER_MIN_FREE_BYTES:-${EMBYMEDIA_WORKER_MIN_FREE_BYTES:-10737418240}}
access_helper=${EMBYMEDIA_NAS_WORKER_ACCESS_HELPER:-${EMBYMEDIA_WORKER_ACCESS_HELPER:-/usr/local/sbin/embymedia-nas-worker-access}}

for path in "$worker_home" "$spool_dir" "$mount_root"; do
  case "$path" in
    /volume1/*) ;;
    *) echo "NAS worker paths must stay below /volume1" >&2; exit 1 ;;
  esac
  case "$path" in
    *[!A-Za-z0-9_./-]*) echo "NAS worker paths must not contain shell metacharacters or spaces" >&2; exit 1 ;;
  esac
done
case "$access_helper" in
  /*) ;;
  *) echo "NAS worker access helper must be absolute" >&2; exit 1 ;;
esac
case "$access_helper" in *[!A-Za-z0-9_./-]*) echo "NAS worker access helper path is invalid" >&2; exit 1 ;; esac
case "$minimum_free" in *[!0-9]*|'') echo 'EMBYMEDIA_NAS_WORKER_MIN_FREE_BYTES must be a nonnegative integer' >&2; exit 1 ;; esac

binary=$source_tree/bin/embymedia-transfer-worker-linux-amd64
checksum=$binary.sha256
test -x "$binary"
test -r "$checksum"
(cd "$(dirname "$binary")" && sha256sum -c "$(basename "$checksum")")

install -m 0700 -d "$worker_home" "$worker_home/bin" "$worker_home/config" "$spool_dir" "$HOME/.ssh"
install -m 0700 "$binary" "$worker_home/bin/embymedia-transfer-worker"
install -m 0600 /dev/null "$config_file"
printf '%s\n' \
  "EMBYMEDIA_WORKER_SPOOL_DIR=$spool_dir" \
  "EMBYMEDIA_WORKER_MOUNT_ROOT=$mount_root" \
  "EMBYMEDIA_WORKER_ACCESS_HELPER=$access_helper" \
  "EMBYMEDIA_WORKER_MIN_FREE_BYTES=$minimum_free" > "$config_file"
install -m 0600 "$source_tree/deploy/nas-worker-compose.yml" "$worker_home/clouddrive-compose.yml"

set -- $(cat "$public_key_file")
[ "$#" -ge 2 ] && [ "$1" = ssh-ed25519 ] || { echo 'controller key must be one ssh-ed25519 public key' >&2; exit 1; }
key_type=$1
key_value=$2
forced_command="$worker_home/bin/embymedia-transfer-worker -spool-dir $spool_dir -mount-root $mount_root -access-helper $access_helper -min-free-bytes $minimum_free"
authorized_keys=$HOME/.ssh/authorized_keys
temporary=$HOME/.ssh/.authorized_keys.embymedia.$$
if [ -f "$authorized_keys" ]; then
  sed '/ embymedia-nas-worker$/d' "$authorized_keys" > "$temporary"
else
  : > "$temporary"
fi
printf '%s\n' "no-agent-forwarding,no-port-forwarding,no-X11-forwarding,no-pty,command=\"$forced_command\" $key_type $key_value embymedia-nas-worker" >> "$temporary"
chmod 0600 "$temporary"
mv -f "$temporary" "$authorized_keys"

printf '%s\n' "$worker_home/bin/embymedia-transfer-worker"
