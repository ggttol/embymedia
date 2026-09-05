#!/bin/sh
set -eu

snapshot=${1:?snapshot id required}
target=${2:?isolated restore target required}
case "$target" in
  /srv/embymedia/restore-*) ;;
  *) echo 'restore target must be /srv/embymedia/restore-*' >&2; exit 1 ;;
esac
[ ! -e "$target" ] || { echo 'restore target already exists' >&2; exit 1; }
mkdir -m 0700 "$target"
RESTIC_PASSWORD_FILE=/etc/embymedia/secrets/restic-local-password \
  restic --repo /srv/embymedia/backups/restic-local restore "$snapshot" --target "$target"

database="$target/srv/embymedia/data/embymedia.db"
test -r "$database"
test -d "$target/srv/embymedia/data/emby/config"
test -d "$target/srv/embymedia/data/clouddrive/config"
test -d "$target/srv/embymedia/data/strm"
test -d "$target/srv/embymedia/data/authelia"

# The check command opens the isolated database, runs monotonic migrations, and starts no workers.
/opt/embymedia-v2/current/bin/embymedia -check-db -db "$database"
test -s "$database"
