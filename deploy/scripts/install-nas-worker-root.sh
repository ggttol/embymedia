#!/bin/sh
set -eu
umask 077

source_tree=${1:?source tree required}
mount_root=${2:-/volume1/docker/clouddrive2/CloudNAS/CloudDrive}
worker_user=${3:-gaotao}
worker_uid=${4:-1026}
worker_gid=${5:-100}
[ "$(id -u)" -eq 0 ] || { echo 'run as root' >&2; exit 1; }
case "$mount_root" in /volume1/*) ;; *) echo 'mount root must stay below /volume1' >&2; exit 1 ;; esac
case "$mount_root" in *[!A-Za-z0-9_./-]*) echo 'mount root must not contain shell metacharacters or spaces' >&2; exit 1 ;; esac
case "$worker_user" in *[!A-Za-z0-9_-]*|'') echo 'worker user is invalid' >&2; exit 1 ;; esac
case "$worker_uid:$worker_gid" in *[!0-9:]*|:|*:|*::*) echo 'worker UID and GID must be numeric' >&2; exit 1 ;; esac
id "$worker_user" >/dev/null
[ "$(id -u "$worker_user")" -eq "$worker_uid" ]
[ "$(id -g "$worker_user")" -eq "$worker_gid" ]
test -d "$mount_root"

install -o root -g root -m 0755 "$source_tree/deploy/scripts/nas-worker-access.sh" /usr/local/sbin/embymedia-nas-worker-access
cat > /etc/embymedia-nas-worker.conf <<EOF
NAS_WORKER_MOUNT_ROOT=$mount_root
NAS_WORKER_UID=$worker_uid
NAS_WORKER_GID=$worker_gid
EOF
chown root:root /etc/embymedia-nas-worker.conf
chmod 0600 /etc/embymedia-nas-worker.conf

if [ ! -d /etc/sudoers.d ]; then install -o root -g root -m 0755 -d /etc/sudoers.d; fi
temporary=$(mktemp /etc/sudoers.d/.embymedia-nas-worker-XXXXXXXX)
printf '%s\n' "$worker_user ALL=(root) NOPASSWD: /usr/local/sbin/embymedia-nas-worker-access" > "$temporary"
chmod 0440 "$temporary"
mv -f "$temporary" /etc/sudoers.d/embymedia-nas-worker
if ! sudo -n -l -U "$worker_user" | grep -Fq '/usr/local/sbin/embymedia-nas-worker-access'; then
  rm -f /etc/sudoers.d/embymedia-nas-worker
  echo 'NAS worker sudoers rule did not validate' >&2
  exit 1
fi
