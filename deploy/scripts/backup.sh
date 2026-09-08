#!/bin/sh
set -eu
umask 077

exec 9>/run/lock/embymedia-backup.lock
flock -w 300 9 || { echo 'timed out waiting for deployment or recovery lock' >&2; exit 1; }

local_repo=/srv/embymedia/backups/restic-local
cache_dir=/srv/embymedia/backups/restic-cache
local_password=/etc/embymedia/secrets/restic-local-password
snapshot=/srv/embymedia/backups/.online-snapshot
build=/srv/embymedia/backups/.online-snapshot-build-$$
login_config=/srv/embymedia/data/auth/http-login.json
snapshot_source_root=${EMBYMEDIA_SNAPSHOT_SOURCE_ROOT:-/}

cleanup() {
  status=$?
  trap - EXIT INT TERM HUP
  rm -rf "$build" "$snapshot"
  exit "$status"
}
trap cleanup EXIT INT TERM HUP

test -r "$local_password"
test -s "$login_config"
test ! -e "$build"
rm -rf "$snapshot"
install -d -m 0700 "$cache_dir"
export RESTIC_CACHE_DIR="$cache_dir"
/usr/bin/python3 /opt/embymedia-v2/current/deploy/scripts/snapshot-live.py --strip-prefix "$snapshot_source_root" --output "$build" \
  --exclude /srv/embymedia/data/emby/config/logs \
  --exclude /srv/embymedia/data/emby/config/cache \
  --exclude /srv/embymedia/data/emby/config/transcoding-temp \
  --exclude /srv/embymedia/data/clouddrive/config/log \
  --exclude /srv/embymedia/data/clouddrive/config/temp \
  --exclude /srv/embymedia/data/clouddrive/config/updates \
  /srv/embymedia/data/embymedia.db \
  "$login_config" \
  /srv/embymedia/data/emby/config \
  /srv/embymedia/data/clouddrive/config \
  /srv/embymedia/data/strm \
  /srv/embymedia/data/authelia \
  /etc/embymedia/authelia \
  /etc/embymedia/secrets/authelia-users.yml
mv "$build" "$snapshot"

if [ ! -d "$local_repo" ]; then
  RESTIC_PASSWORD_FILE="$local_password" restic init --repo "$local_repo"
fi
RESTIC_PASSWORD_FILE="$local_password" restic --repo "$local_repo" backup \
  --ignore-ctime --ignore-inode \
  "$snapshot" \
  --tag embymedia-v2-daily
RESTIC_PASSWORD_FILE="$local_password" restic --repo "$local_repo" forget --tag embymedia-v2-daily --group-by host,tags --keep-daily 14 --prune
