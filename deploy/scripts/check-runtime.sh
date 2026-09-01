#!/bin/sh
set -eu

: "${EMBYMEDIA_PRESET_ROOT:?EMBYMEDIA_PRESET_ROOT is required}"
: "${EMBYMEDIA_DATABASE_URL:?EMBYMEDIA_DATABASE_URL is required}"

pg_isready -h 127.0.0.1 -p 5432 -U embymedia -d embymedia >/dev/null
mountpoint -q /srv/embymedia/data/clouddrive/CloudNAS/CloudDrive
timeout 5 stat /srv/embymedia/data/clouddrive/CloudNAS/CloudDrive/.embymedia-health-canary >/dev/null
curl -fsS --max-time 5 http://127.0.0.1:8096/System/Info/Public >/dev/null
curl -fsS --max-time 5 http://127.0.0.1:9091/api/health >/dev/null
test -d /srv/embymedia/data/strm
test -w /srv/embymedia/data/strm
test -r "$EMBYMEDIA_PRESET_ROOT/emby-operator/preset.yml"
test -r "$EMBYMEDIA_PRESET_ROOT/emby-operator/agent.cordis.yml"
credentials=/srv/embymedia/data/dsh/.credentials.yaml
if [ -e "$credentials" ]; then
  test "$(stat -c '%a' "$credentials")" = 600
  test "$(stat -c '%U:%G' "$credentials")" = embymedia:embymedia
fi
test ! -S /var/run/docker.sock || test ! -r /var/run/docker.sock
