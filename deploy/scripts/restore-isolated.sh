#!/bin/sh
set -eu

repo=/opt/embymedia/current
snapshot=${1:?snapshot id required}
target=${2:?isolated restore target required}
case "$target" in
  /srv/embymedia/restore-*) ;;
  *) echo 'restore target must be /srv/embymedia/restore-*' >&2; exit 1 ;;
esac
[ ! -e "$target" ] || { echo 'restore target already exists' >&2; exit 1; }
database=embymedia_restore_gate
database_created=0

# Run a postgres client inside the postgres container, sourcing the DB password
# from the container's own secret file so it never appears in a process argv.
pg() {
  /usr/bin/docker exec "$pg_container" sh -c 'export PGPASSWORD=$(cat /run/secrets/postgres_password); exec "$@"' sh "$@"
}

cleanup() {
  if [ "$database_created" -eq 1 ]; then
    pg dropdb --if-exists -h 127.0.0.1 -U embymedia "$database"
  fi
}
trap cleanup EXIT
trap 'exit 1' INT TERM
mkdir -m 0700 "$target"
RESTIC_PASSWORD_FILE=/etc/embymedia/secrets/restic-local-password \
  restic --repo /srv/embymedia/backups/restic-local restore "$snapshot" --target "$target"
dump="$target/srv/embymedia/backups/stage/postgres.dump"
test -r "$dump"
test -d "$target/srv/embymedia/data/dsh"
test -d "$target/srv/embymedia/data/emby/config"
test -d "$target/srv/embymedia/data/clouddrive/config"
test -d "$target/srv/embymedia/data/strm"
test -d "$target/srv/embymedia/data/authelia"
pg_container=$(/usr/bin/docker compose --env-file /etc/embymedia/stack.env -f "$repo/deploy/compose.yml" ps -q postgres)
pg dropdb --if-exists -h 127.0.0.1 -U embymedia "$database"
pg createdb -h 127.0.0.1 -U embymedia "$database"
database_created=1
/usr/bin/docker exec -i "$pg_container" sh -c 'export PGPASSWORD=$(cat /run/secrets/postgres_password); exec "$@"' sh \
  pg_restore --exit-on-error --no-owner --no-privileges -h 127.0.0.1 -U embymedia -d "$database" < "$dump"
count=$(pg psql -h 127.0.0.1 -U embymedia -d "$database" -Atc 'SELECT count(*) FROM embymedia_schema_migrations')
case "$count" in ''|0|*[!0-9]*) echo 'restored database migration ledger is empty' >&2; exit 1 ;; esac
pg dropdb -h 127.0.0.1 -U embymedia "$database"
database_created=0
