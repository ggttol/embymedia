#!/bin/sh
set -eu

artifacts=${1:?artifact directory required}
. /etc/embymedia/stack.env
repo=/opt/embymedia/current
manifest="$artifacts/manifest.json"
test -r "$manifest"
python3 - "$artifacts" <<'PY'
import hashlib, json, pathlib, sys
root = pathlib.Path(sys.argv[1]).resolve()
manifest = json.loads((root / 'manifest.json').read_text())
for name, expected in manifest['artifacts'].items():
    path = root / name
    digest = hashlib.sha256(path.read_bytes()).hexdigest()
    if path.stat().st_size != expected['bytes'] or digest != expected['sha256']:
        raise SystemExit(f'artifact mismatch: {name}')
PY

for archive in clouddrive-image.tar emby-image.tar postgres-image.tar authelia-image.tar; do
  /usr/bin/docker load -i "$artifacts/$archive" >/dev/null
done
expected_clouddrive_image=$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["imageIds"]["clouddrive2"])' "$manifest")
expected_emby_image=$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["imageIds"]["emby"])' "$manifest")
expected_postgres_image=$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["imageIds"]["postgres"])' "$manifest")
expected_authelia_image=$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["imageIds"]["authelia"])' "$manifest")
test "$(/usr/bin/docker image inspect --format '{{.Id}}' "${CLOUDDRIVE_IMAGE:?}")" = "$expected_clouddrive_image" || { echo "CloudDrive image identity mismatch" >&2; exit 1; }
test "$(/usr/bin/docker image inspect --format '{{.Id}}' "${EMBY_IMAGE:?}")" = "$expected_emby_image" || { echo "Emby image identity mismatch" >&2; exit 1; }
test "$(/usr/bin/docker image inspect --format '{{.Id}}' "${POSTGRES_IMAGE:?}")" = "$expected_postgres_image" || { echo "PostgreSQL image identity mismatch" >&2; exit 1; }
test "$(/usr/bin/docker image inspect --format '{{.Id}}' "${AUTHELIA_IMAGE:?}")" = "$expected_authelia_image" || { echo "Authelia image identity mismatch" >&2; exit 1; }

for directory in \
  /srv/embymedia/data/emby/config \
  /srv/embymedia/data/clouddrive/config \
  /srv/embymedia/data/strm; do
  test -d "$directory"
  test -z "$(ls -A "$directory")" || { echo "$directory must be empty" >&2; exit 1; }
done

stage=$(mktemp -d /srv/embymedia/import.XXXXXX)
trap 'rm -rf "$stage"' EXIT INT TERM
mkdir "$stage/emby" "$stage/clouddrive" "$stage/strm"
tar -xf "$artifacts/emby-config.tar" -C "$stage/emby"
tar -xf "$artifacts/clouddrive-config.tar" -C "$stage/clouddrive"
tar -xf "$artifacts/strm.tar" -C "$stage/strm"
rm -rf "$stage/clouddrive/updates"
if grep -RIl '/volume1' "$stage" | grep -q .; then
  echo 'unmapped /volume1 reference remains in imported artifacts' >&2
  exit 1
fi
cp -a "$stage/emby/." /srv/embymedia/data/emby/config/
cp -a "$stage/clouddrive/." /srv/embymedia/data/clouddrive/config/
cp -a "$stage/strm/." /srv/embymedia/data/strm/
chown -R "${MEDIA_UID:?}:${MEDIA_GID:?}" /srv/embymedia/data/emby/config /srv/embymedia/data/clouddrive/config
chown -R embymedia:embymedia /srv/embymedia/data/strm

/usr/bin/docker compose --env-file /etc/embymedia/stack.env -f "$repo/deploy/compose.yml" up -d --wait postgres
pg_container=$(/usr/bin/docker compose --env-file /etc/embymedia/stack.env -f "$repo/deploy/compose.yml" ps -q postgres)
pg() {
  /usr/bin/docker exec "$pg_container" sh -c 'export PGPASSWORD=$(cat /run/secrets/postgres_password); exec "$@"' sh "$@"
}
pg dropdb --if-exists -h 127.0.0.1 -U embymedia embymedia_legacy
pg createdb -h 127.0.0.1 -U embymedia embymedia_legacy
/usr/bin/docker exec -i "$pg_container" sh -c 'export PGPASSWORD=$(cat /run/secrets/postgres_password); exec "$@"' sh \
  pg_restore -h 127.0.0.1 -U embymedia -d embymedia_legacy --no-owner --no-privileges \
  < "$artifacts/legacy-database.dump"

runuser -u embymedia -- /usr/local/bin/node /opt/embymedia/current/apps/embymedia-migrate/lib/bin.js inventory \
  --database-url-file /etc/embymedia/secrets/legacy-database-url \
  --dsh-home /srv/embymedia/data/dsh \
  --output /opt/embymedia/current/migration/database-inventory.json
runuser -u embymedia -- /usr/local/bin/node /opt/embymedia/current/apps/embymedia-migrate/lib/bin.js import \
  --database-url-file /etc/embymedia/secrets/legacy-database-url \
  --target-url-file /etc/embymedia/secrets/database-url \
  --deepseek-api-key-file /etc/embymedia/secrets/deepseek-api-key \
  --dsh-home /srv/embymedia/data/dsh \
  --report /opt/embymedia/current/migration/import-report.json
runuser -u embymedia -- /usr/local/bin/node /opt/embymedia/current/apps/embymedia-migrate/lib/bin.js verify \
  --source /opt/embymedia/current/migration/database-inventory.json \
  --target-url-file /etc/embymedia/secrets/database-url \
  --dsh-home /srv/embymedia/data/dsh \
  --report /opt/embymedia/current/migration/verify-report.json
