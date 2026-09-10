#!/bin/sh
set -eu
repo=$(CDPATH= cd -- "$(dirname "$0")/../.." && pwd)

[ "$(id -u)" -eq 0 ] || { echo 'run as root' >&2; exit 1; }
[ "$(dpkg --print-architecture)" = amd64 ] || { echo 'embymedia requires Debian amd64' >&2; exit 1; }
. /etc/os-release
[ "${ID:-}" = debian ] && [ "${VERSION_ID%%.*}" = 13 ] || { echo 'embymedia requires Debian 13' >&2; exit 1; }

export DEBIAN_FRONTEND=noninteractive
apt-get update
apt-get install -y ca-certificates curl gnupg caddy restic util-linux nftables rsync python3 acl
rm -f /etc/apt/sources.list.d/docker.list /etc/apt/keyrings/docker.asc
apt-get update
apt-get install -y docker.io docker-compose


if ! getent group embymedia >/dev/null; then groupadd --system embymedia; fi
if ! id embymedia >/dev/null 2>&1; then
  useradd --system --gid embymedia --home-dir /srv/embymedia --no-create-home --shell /usr/sbin/nologin embymedia
fi

install -o root -g root -m 0755 -d /opt/embymedia-v2 /opt/embymedia-v2/releases /etc/embymedia /etc/embymedia/authelia
install -o root -g embymedia -m 0750 -d /etc/embymedia/secrets
install -o root -g root -m 0644 "$repo/deploy/authelia/configuration.yml" /etc/embymedia/authelia/configuration.yml
install -o root -g root -m 0644 "$repo/deploy/caddy/Caddyfile" /etc/caddy/Caddyfile
install -o root -g root -m 0644 "$repo/deploy/nftables.conf" /etc/nftables.conf
nft --check --file /etc/nftables.conf
install -o root -g root -m 0644 "$repo"/deploy/systemd/*.service "$repo"/deploy/systemd/*.timer /etc/systemd/system/ 2>/dev/null || true
install -o embymedia -g embymedia -m 0750 -d \
  /srv/embymedia \
  /srv/embymedia/data \
  /srv/embymedia/data/auth \
  /srv/embymedia/data/emby \
  /srv/embymedia/data/emby/config \
  /srv/embymedia/data/clouddrive \
  /srv/embymedia/data/clouddrive/config \
  /srv/embymedia/data/clouddrive/CloudNAS \
  /srv/embymedia/data/clouddrive/update-disabled \
  /srv/embymedia/data/strm-v2 \
  /srv/embymedia/data/authelia \
  /srv/embymedia/backups

find /etc/embymedia/secrets -type f -exec chown root:embymedia {} + -exec chmod 0640 {} +
chown -R embymedia:embymedia /srv/embymedia/data/strm-v2
systemctl daemon-reload
systemctl enable docker.service containerd.service caddy.service nftables.service
