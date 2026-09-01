#!/bin/sh
set -eu

artifacts=${1:?artifact directory required}
[ "$(id -u)" -eq 0 ] || { echo 'run as root' >&2; exit 1; }

secrets=/etc/embymedia/secrets
repo=/opt/embymedia/current
manifest="$artifacts/manifest.json"
test -r "$manifest"

install -d -o root -g embymedia -m 0750 "$secrets"

# Fresh random machine secrets (regenerated only on first bootstrap).
if [ ! -e "$secrets/postgres-password" ]; then
  umask 077
  openssl rand -hex 32 > "$secrets/postgres-password"
  openssl rand -hex 48 > "$secrets/authelia-jwt-secret"
  openssl rand -hex 48 > "$secrets/authelia-session-secret"
  openssl rand -hex 48 > "$secrets/authelia-storage-key"
  openssl rand -hex 32 > "$secrets/restic-local-password"
  chown root:embymedia "$secrets"/postgres-password "$secrets"/authelia-jwt-secret "$secrets"/authelia-session-secret "$secrets"/authelia-storage-key "$secrets"/restic-local-password
fi

postgres_password=$(cat "$secrets/postgres-password")

# Database connection strings for the target and the restored legacy database.
printf 'postgres://embymedia:%s@127.0.0.1:5432/embymedia\n' "$postgres_password" > "$secrets/database-url"
printf 'postgres://embymedia:%s@127.0.0.1:5432/embymedia_legacy\n' "$postgres_password" > "$secrets/legacy-database-url"

# Bootstrap administrator password for Authelia; replaced at cutover.
if [ ! -e "$secrets/admin-bootstrap-password" ]; then
  umask 077
  openssl rand -base64 18 > /tmp/embymedia-admin-password
  mv /tmp/embymedia-admin-password "$secrets/admin-bootstrap-password"
fi

# DeepSeek API key: written once from the operator-supplied environment on first
# bootstrap, then held only as the DEEPSEEK_API_KEY credential ref. The provider
# must not be shadowed by a read-only env var, so it never lands in dsh.env.
if [ -n "${DEEPSEEK_API_KEY:-}" ]; then
  printf '%s\n' "$DEEPSEEK_API_KEY" > "$secrets/deepseek-api-key"
elif [ ! -s "$secrets/deepseek-api-key" ]; then
  echo 'DEEPSEEK_API_KEY must be provided on first bootstrap' >&2
  exit 1
fi

# Authelia file user database: generate an Argon2id hash from the bootstrap
# password using the pinned Authelia image, then write the users_database.
if [ ! -e "$secrets/authelia-users.yml" ]; then
  /usr/bin/docker load -i "$artifacts/authelia-image.tar" >/dev/null
  hash=$(/usr/bin/docker run --rm \
    -v "$secrets/admin-bootstrap-password:/run/admin-password:ro" \
    --entrypoint /bin/sh authelia/authelia:4 \
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

# Use immutable image IDs from the already verified migration manifest. Docker
# accepts sha256:<id> references directly, so later tag drift cannot change the
# image selected by Compose.
image_id() {
  python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["imageIds"][sys.argv[2]])' "$manifest" "$1"
}
cat > /etc/embymedia/stack.env <<EOF
POSTGRES_IMAGE=$(image_id postgres)
CLOUDDRIVE_IMAGE=$(image_id clouddrive2)
EMBY_IMAGE=$(image_id emby)
AUTHELIA_IMAGE=$(image_id authelia)
MEDIA_UID=1026
MEDIA_GID=100
CLOUDDRIVE_ADMIN_PORT=19798
EOF
chown root:root /etc/embymedia/stack.env
chmod 0640 /etc/embymedia/stack.env

cat > /etc/embymedia/dsh.env <<EOF
EMBYMEDIA_DATABASE_URL=postgres://embymedia:${postgres_password}@127.0.0.1:5432/embymedia
EMBYMEDIA_WRITE_MODE=staging
EMBYMEDIA_SCHEDULER_ENABLED=0
EMBYMEDIA_ALLOW_INSECURE_RESOURCE_API=0
EMBYMEDIA_APPROVAL_MODE=manual
EOF
chown root:embymedia /etc/embymedia/dsh.env
chmod 0640 /etc/embymedia/dsh.env

chmod 0640 "$secrets"/database-url "$secrets"/legacy-database-url "$secrets"/deepseek-api-key
chown root:embymedia "$secrets"/database-url "$secrets"/legacy-database-url "$secrets"/deepseek-api-key

echo '{"init":"ok","adminPassword":"/etc/embymedia/secrets/admin-bootstrap-password"}'
