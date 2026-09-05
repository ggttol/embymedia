#!/bin/sh
set -eu

source_tree=${1:?source tree required}
release_id=${2:-$(date -u +%Y%m%dT%H%M%SZ)}

[ "$(id -u)" -eq 0 ] || { echo 'run as root' >&2; exit 1; }
case "$release_id" in
  *[!A-Za-z0-9._-]*|'') echo 'invalid release id' >&2; exit 1 ;;
esac
source_tree=$(realpath "$source_tree")
release=/opt/embymedia-v2/releases/$release_id
binary="$source_tree/bin/embymedia-linux-amd64"
checksum="$binary.sha256"
database=/srv/embymedia/data/embymedia.db
test -x "$binary"
test -r "$checksum"
test ! -e "$release"
(cd "$(dirname "$binary")" && sha256sum -c "$(basename "$checksum")")

mkdir -p "$release/bin"
trap 'rm -rf "$release"' EXIT INT TERM
install -o root -g root -m 0755 "$binary" "$release/bin/embymedia"
cp -R "$source_tree/deploy" "$release/deploy"
chown -R root:root "$release"
chmod 0755 "$release" "$release/bin" "$release/bin/embymedia"

systemctl stop embymedia-v2.service 2>/dev/null || true
systemctl disable --now embymedia-dsh.service embymedia-control-helper.service 2>/dev/null || true
"$release/bin/embymedia" -check-db -db "$database"
"$release/bin/embymedia" -bootstrap-webhook-secret-file /etc/embymedia/secrets/clouddrive-webhook-secret -db "$database"
login_config=/srv/embymedia/data/auth/http-login.json
install -o embymedia -g embymedia -m 0700 -d /srv/embymedia/data/auth
if [ ! -s "$login_config" ]; then
  if [ -s /etc/embymedia/http-login.json ]; then
    install -o embymedia -g embymedia -m 0600 /etc/embymedia/http-login.json "$login_config"
  else
    /usr/bin/python3 "$release/deploy/scripts/init-http-login.py" --password-file /etc/embymedia/secrets/admin-bootstrap-password --output "$login_config"
    chown embymedia:embymedia "$login_config"
  fi
fi
previous=$(readlink -f /opt/embymedia-v2/current 2>/dev/null || true)
ln -sfn "$release" /opt/embymedia-v2/current.next
mv -Tf /opt/embymedia-v2/current.next /opt/embymedia-v2/current

install -o root -g root -m 0644 "$release"/deploy/systemd/*.service "$release"/deploy/systemd/*.timer /etc/systemd/system/
install -o root -g root -m 0644 "$release/deploy/caddy/Caddyfile" /etc/caddy/Caddyfile
skill_source="$release/deploy/hermes/skills/embymedia-v2-operator/SKILL.md"
install -o gaotao -g gaotao -m 0755 -d /home/gaotao/.hermes/skills/embymedia-v2-operator
install -o gaotao -g gaotao -m 0644 "$skill_source" /home/gaotao/.hermes/skills/embymedia-v2-operator/SKILL.md

systemctl daemon-reload
systemctl enable --now embymedia-stack.service embymedia-backup.timer
if ! systemctl enable --now embymedia-http-login.service embymedia-v2.service \
  || ! systemctl restart embymedia-http-login.service \
  || ! systemctl restart embymedia-v2.service; then
  ready=false
else
  ready=false
  attempts=0
  while [ "$attempts" -lt 30 ]; do
    if curl -fsS --max-time 2 http://127.0.0.1:9092/health >/dev/null \
      && curl -fsS --max-time 2 -H 'Remote-User: release-probe' http://127.0.0.1:3080/api/v1/openapi.json >/dev/null; then
      ready=true
      break
    fi
    attempts=$((attempts + 1))
    sleep 1
  done
fi
if [ "$ready" != true ]; then
  if [ -n "$previous" ] && [ -x "$previous/bin/embymedia" ]; then
    ln -sfn "$previous" /opt/embymedia-v2/current
    systemctl restart embymedia-http-login.service || true
    systemctl restart embymedia-v2.service || true
  else
    rm -f /opt/embymedia-v2/current
    systemctl stop embymedia-http-login.service embymedia-v2.service || true
  fi
  exit 1
fi
systemctl restart caddy.service
trap - EXIT INT TERM
printf '%s\n' "$release"
