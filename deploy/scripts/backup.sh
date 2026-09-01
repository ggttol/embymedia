#!/bin/sh
set -eu

exec 9>/run/lock/embymedia-backup.lock
flock -n 9 || exit 0

repo=/opt/embymedia/current
local_repo=/srv/embymedia/backups/restic-local
local_password=/etc/embymedia/secrets/restic-local-password
stage=/srv/embymedia/backups/stage
rm -rf "$stage"
install -o root -g root -m 0700 -d "$stage"
restored=0
services_stopped=0

restore_services() {
  if [ "$restored" -eq 1 ]; then return; fi
  if [ "$services_stopped" -eq 0 ]; then return; fi
  restored=1
  systemctl restart embymedia-stack.service
  systemctl start embymedia-dsh.service
}
cleanup() {
  rm -rf "$stage"
  restore_services
}
trap cleanup EXIT
trap 'exit 1' INT TERM
for file in "$local_password" /etc/embymedia/secrets/postgres-password; do
  test -r "$file"
done

services_stopped=1
systemctl stop embymedia-dsh.service
/usr/bin/docker compose --env-file /etc/embymedia/stack.env -f "$repo/deploy/compose.yml" stop emby clouddrive2 authelia

pg_container=$(/usr/bin/docker compose --env-file /etc/embymedia/stack.env -f "$repo/deploy/compose.yml" ps -q postgres)
/usr/bin/docker exec "$pg_container" sh -c 'exec pg_dump -Fc -U "$POSTGRES_USER" "$POSTGRES_DB"' > "$stage/postgres.dump"
chmod 0600 "$stage/postgres.dump"

if [ ! -d "$local_repo" ]; then
  RESTIC_PASSWORD_FILE="$local_password" restic init --repo "$local_repo"
fi
RESTIC_PASSWORD_FILE="$local_password" restic --repo "$local_repo" backup \
  "$stage/postgres.dump" \
  /srv/embymedia/data/dsh \
  /srv/embymedia/data/emby/config \
  /srv/embymedia/data/clouddrive/config \
  /srv/embymedia/data/strm \
  /srv/embymedia/data/authelia \
  /etc/embymedia/authelia \
  /etc/embymedia/secrets/authelia-users.yml \
  --tag embymedia-daily
RESTIC_PASSWORD_FILE="$local_password" restic --repo "$local_repo" forget --tag embymedia-daily --group-by host,tags --keep-daily 14 --prune
rm -rf "$stage"

restore_services
