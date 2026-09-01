#!/bin/sh
set -eu

artifacts=${1:?final artifact directory required}
inventory=${2:?source inventory required}
repo=/opt/embymedia/current
. /etc/embymedia/stack.env

test "$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["phase"])' "$artifacts/manifest.json")" = final
python3 - "$artifacts" <<'PY'
import hashlib, json, pathlib, sys
root = pathlib.Path(sys.argv[1]).resolve()
manifest = json.loads((root / 'manifest.json').read_text())
for name, expected in manifest['artifacts'].items():
    path = root / name
    if path.stat().st_size != expected['bytes'] or hashlib.sha256(path.read_bytes()).hexdigest() != expected['sha256']:
        raise SystemExit(f'artifact mismatch: {name}')
PY

systemctl stop caddy.service embymedia-dsh.service embymedia-control-helper.service embymedia-stack.service || true
stage=$(mktemp -d /srv/embymedia/final.XXXXXX)
trap 'rm -rf "$stage"' EXIT INT TERM
mkdir "$stage/emby" "$stage/clouddrive" "$stage/strm"
tar -xf "$artifacts/emby-config.tar" -C "$stage/emby"
tar -xf "$artifacts/clouddrive-config.tar" -C "$stage/clouddrive"
tar -xf "$artifacts/strm.tar" -C "$stage/strm"
rm -rf "$stage/clouddrive/updates"
rsync -a --delete "$stage/emby/" /srv/embymedia/data/emby/config/
rsync -a --delete "$stage/clouddrive/" /srv/embymedia/data/clouddrive/config/
rsync -a --delete "$stage/strm/" /srv/embymedia/data/strm/
chown -R "${MEDIA_UID:?}:${MEDIA_GID:?}" /srv/embymedia/data/emby/config /srv/embymedia/data/clouddrive/config
chown -R embymedia:embymedia /srv/embymedia/data/strm
python3 "$repo/migration/disable-webhook.py" \
  --toml /srv/embymedia/data/clouddrive/config/webhooks/webhook.toml \
  --inventory "$inventory"

/usr/bin/docker compose --env-file /etc/embymedia/stack.env -f "$repo/deploy/compose.yml" up -d --wait postgres
pg_container=$(/usr/bin/docker compose --env-file /etc/embymedia/stack.env -f "$repo/deploy/compose.yml" ps -q postgres)
pg_password=$(cat /etc/embymedia/secrets/postgres-password)
/usr/bin/docker exec -e PGPASSWORD="$pg_password" "$pg_container" dropdb --if-exists -h 127.0.0.1 -U embymedia embymedia_legacy
/usr/bin/docker exec -e PGPASSWORD="$pg_password" "$pg_container" createdb -h 127.0.0.1 -U embymedia embymedia_legacy
/usr/bin/docker exec -i -e PGPASSWORD="$pg_password" "$pg_container" \
  pg_restore -h 127.0.0.1 -U embymedia -d embymedia_legacy --no-owner --no-privileges \
  < "$artifacts/legacy-database.dump"
runuser -u embymedia -- /usr/local/bin/node "$repo/apps/embymedia-migrate/lib/bin.js" inventory \
  --database-url-file /etc/embymedia/secrets/legacy-database-url \
  --dsh-home /srv/embymedia/data/dsh \
  --output "$repo/migration/database-inventory.json"
runuser -u embymedia -- /usr/local/bin/node "$repo/apps/embymedia-migrate/lib/bin.js" import \
  --database-url-file /etc/embymedia/secrets/legacy-database-url \
  --target-url-file /etc/embymedia/secrets/database-url \
  --deepseek-api-key-file /etc/embymedia/secrets/deepseek-api-key \
  --dsh-home /srv/embymedia/data/dsh \
  --report "$repo/migration/final-import-report.json"
runuser -u embymedia -- /usr/local/bin/node "$repo/apps/embymedia-migrate/lib/bin.js" verify \
  --source "$repo/migration/database-inventory.json" \
  --target-url-file /etc/embymedia/secrets/database-url \
  --dsh-home /srv/embymedia/data/dsh \
  --report "$repo/migration/final-verify-report.json"

sed -i '/^EMBYMEDIA_WRITE_MODE=/d;/^EMBYMEDIA_SCHEDULER_ENABLED=/d' /etc/embymedia/dsh.env
printf '%s\n' 'EMBYMEDIA_WRITE_MODE=staging' 'EMBYMEDIA_SCHEDULER_ENABLED=0' >> /etc/embymedia/dsh.env
systemctl start embymedia-stack.service embymedia-control-helper.service embymedia-dsh.service
ready=0
for _ in $(seq 1 30); do
  if curl -fsS --max-time 3 http://127.0.0.1:3080/internal/embymedia/health >/dev/null 2>&1; then
    ready=1
    break
  fi
  sleep 2
done
[ "$ready" -eq 1 ] || { echo 'DSH did not become ready within 60 seconds' >&2; exit 1; }
python3 "$repo/migration/probe-webhook.py" --toml /srv/embymedia/data/clouddrive/config/webhooks/webhook.toml
caddy validate --config /etc/caddy/Caddyfile
systemctl start caddy.service
printf '%s\n' '{"state":"staging","scheduler":false,"watcher":false}' > "$repo/migration/cutover-pending.json"
