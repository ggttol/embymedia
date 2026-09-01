#!/bin/sh
set -eu
repo=$(CDPATH= cd -- "$(dirname "$0")/../.." && pwd)

[ "$(id -u)" -eq 0 ] || { echo 'run as root' >&2; exit 1; }
[ "$(dpkg --print-architecture)" = amd64 ] || { echo 'embymedia requires Debian amd64' >&2; exit 1; }
. /etc/os-release
[ "${ID:-}" = debian ] && [ "${VERSION_ID%%.*}" = 13 ] || { echo 'embymedia requires Debian 13' >&2; exit 1; }

export DEBIAN_FRONTEND=noninteractive
apt-get update
apt-get install -y ca-certificates curl gnupg caddy restic postgresql-client util-linux xz-utils nftables rsync build-essential python3 pkg-config
rm -f /etc/apt/sources.list.d/docker.list /etc/apt/keyrings/docker.asc
apt-get update
apt-get install -y docker.io docker-compose

node_version=22.19.0
node_archive="node-v${node_version}-linux-x64.tar.xz"
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT INT TERM
curl -fsSLO --output-dir "$work" "https://nodejs.org/dist/v${node_version}/${node_archive}"
curl -fsSLo "$work/SHASUMS256.txt" "https://nodejs.org/dist/v${node_version}/SHASUMS256.txt"
(
  cd "$work"
  checksum=$(sed -n "s/  ${node_archive}$//p" SHASUMS256.txt)
  [ -n "$checksum" ] || { echo 'Node checksum missing' >&2; exit 1; }
  printf '%s  %s\n' "$checksum" "$node_archive" | sha256sum -c -
)
rm -rf /opt/node-22.19.0
mkdir -p /opt/node-22.19.0
ln -sfn /opt/node-22.19.0/bin/node /usr/local/bin/node
ln -sfn /opt/node-22.19.0/bin/corepack /usr/local/bin/corepack
ln -sfn /opt/node-22.19.0/bin/npm /usr/local/bin/npm
ln -sfn /opt/node-22.19.0/bin/npx /usr/local/bin/npx
corepack enable --install-directory /usr/local/bin
corepack prepare pnpm@11.7.0 --activate
[ "$(node --version)" = v22.19.0 ]
[ "$(pnpm --version)" = 11.7.0 ]

if ! getent group embymedia >/dev/null; then groupadd --system embymedia; fi
if ! id embymedia >/dev/null 2>&1; then
  useradd --system --gid embymedia --home-dir /srv/embymedia --no-create-home --shell /usr/sbin/nologin embymedia
fi

install -o root -g root -m 0755 -d /opt/embymedia /opt/embymedia/releases /etc/embymedia /etc/embymedia/authelia
install -o root -g embymedia -m 0750 -d /etc/embymedia/secrets
install -o root -g root -m 0644 "$repo/deploy/authelia/configuration.yml" /etc/embymedia/authelia/configuration.yml
install -o root -g root -m 0644 "$repo/deploy/caddy/Caddyfile" /etc/caddy/Caddyfile
install -o root -g root -m 0644 "$repo/deploy/tmpfiles.d/embymedia.conf" /etc/tmpfiles.d/embymedia.conf
install -o root -g root -m 0644 "$repo/deploy/nftables.conf" /etc/nftables.conf
nft --check --file /etc/nftables.conf
install -o root -g root -m 0644 "$repo"/deploy/systemd/*.service "$repo"/deploy/systemd/*.timer /etc/systemd/system/ 2>/dev/null || true
install -o embymedia -g embymedia -m 0750 -d \
  /srv/embymedia \
  /srv/embymedia/data \
  /srv/embymedia/data/dsh \
  /srv/embymedia/data/postgres \
  /srv/embymedia/data/emby \
  /srv/embymedia/data/emby/config \
  /srv/embymedia/data/clouddrive \
  /srv/embymedia/data/clouddrive/config \
  /srv/embymedia/data/clouddrive/CloudNAS \
  /srv/embymedia/data/clouddrive/update-disabled \
  /srv/embymedia/data/strm \
  /srv/embymedia/data/authelia \
  /srv/embymedia/backups
install -o root -g embymedia -m 0750 -d /run/embymedia-control

find /etc/embymedia/secrets -type f -exec chown root:embymedia {} + -exec chmod 0640 {} +
chown -R embymedia:embymedia /srv/embymedia/data/dsh /srv/embymedia/data/strm
chmod 0700 /srv/embymedia/data/dsh
systemd-tmpfiles --create /etc/tmpfiles.d/embymedia.conf
systemctl daemon-reload
systemctl enable docker.service containerd.service caddy.service nftables.service
