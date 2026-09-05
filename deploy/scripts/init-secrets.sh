#!/bin/sh
set -eu

artifacts=${1:?artifact directory required}
script_dir=$(CDPATH= cd -- "$(dirname "$0")" && pwd)
[ "$(id -u)" -eq 0 ] || { echo 'run as root' >&2; exit 1; }

secrets=/etc/embymedia/secrets
manifest="$artifacts/manifest.json"
test -r "$manifest"
install -d -o root -g embymedia -m 0750 "$secrets"

image_id() {
  python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["imageIds"][sys.argv[2]])' "$manifest" "$1"
}

ensure_hex_secret() {
  file=$1
  bytes=$2
  if [ ! -s "$file" ]; then
    umask 077
    openssl rand -hex "$bytes" > "$file"
  fi
}
ensure_hex_secret "$secrets/authelia-jwt-secret" 48
ensure_hex_secret "$secrets/authelia-session-secret" 48
ensure_hex_secret "$secrets/authelia-storage-key" 48
ensure_hex_secret "$secrets/restic-local-password" 32
ensure_hex_secret "$secrets/clouddrive-webhook-secret" 32
chown root:embymedia "$secrets"/authelia-jwt-secret "$secrets"/authelia-session-secret "$secrets"/authelia-storage-key "$secrets"/restic-local-password "$secrets"/clouddrive-webhook-secret

if [ ! -e "$secrets/admin-bootstrap-password" ]; then
  umask 077
  openssl rand -base64 18 > "$secrets/admin-bootstrap-password"
fi

if [ ! -e "$secrets/authelia-users.yml" ]; then
  /usr/bin/docker load -i "$artifacts/authelia-image.tar" >/dev/null
  authelia_image=$(image_id authelia)
  hash=$(/usr/bin/docker run --rm \
    -v "$secrets/admin-bootstrap-password:/run/admin-password:ro" \
    --entrypoint /bin/sh "$authelia_image" \
    -c 'exec authelia crypto hash generate argon2 --password "$(tr -d "\n" < /run/admin-password)" --no-confirm' \
    2>/dev/null | sed -E 's/^Digest: //')
  cat > "$secrets/authelia-users.yml" <<EOF
users:
  gaotao:
    displayname: gaotao
    password: ${hash}
    email: gaotao@gaotao.cc
    groups:
      - admins
EOF
fi
chown root:embymedia "$secrets/authelia-users.yml"
chmod 0640 "$secrets/authelia-users.yml"
install -o root -g root -m 0640 "$secrets/authelia-users.yml" /etc/embymedia/authelia/users_database.yml

login_config=/srv/embymedia/data/auth/http-login.json
install -o embymedia -g embymedia -m 0700 -d /srv/embymedia/data/auth
if [ ! -s "$login_config" ]; then
  if [ -s /etc/embymedia/http-login.json ]; then
    install -o embymedia -g embymedia -m 0600 /etc/embymedia/http-login.json "$login_config"
  else
    python3 "$script_dir/init-http-login.py" --password-file "$secrets/admin-bootstrap-password" --output "$login_config"
  fi
fi
chown embymedia:embymedia "$login_config"
chmod 0600 "$login_config"

webhook_secret=$(cat "$secrets/clouddrive-webhook-secret")
install -o 1026 -g 100 -m 0755 -d /srv/embymedia/data/clouddrive/config/webhooks
cat > /srv/embymedia/data/clouddrive/config/webhooks/webhook.toml <<EOF
[file_system_watcher]
enabled = true
url = "http://host.docker.internal/hooks/clouddrive2?key=${webhook_secret}"
method = "POST"
EOF
chown 1026:100 /srv/embymedia/data/clouddrive/config/webhooks/webhook.toml
chmod 0644 /srv/embymedia/data/clouddrive/config/webhooks/webhook.toml

cat > /etc/embymedia/stack.env <<EOF
CLOUDDRIVE_IMAGE=$(image_id clouddrive2)
EMBY_IMAGE=$(image_id emby)
AUTHELIA_IMAGE=$(image_id authelia)
MEDIA_UID=1026
MEDIA_GID=100
CLOUDDRIVE_ADMIN_PORT=19798
EOF
chown root:root /etc/embymedia/stack.env
chmod 0640 /etc/embymedia/stack.env

cat > /etc/embymedia/v2.env <<EOF
RESOURCE_INDEX_URL=http://127.0.0.1:8100
EOF
chown root:embymedia /etc/embymedia/v2.env
chmod 0640 /etc/embymedia/v2.env

echo '{"init":"ok","adminPassword":"/etc/embymedia/secrets/admin-bootstrap-password"}'
