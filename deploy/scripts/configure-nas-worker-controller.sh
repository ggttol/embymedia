#!/bin/sh
set -eu
umask 077

target=${1:?SSH target user@host required}
port=${2:?SSH port required}
known_hosts_input=${3:?verified known_hosts file required}
case "$target" in
  *@*) ;;
  *) echo 'SSH target must be user@host' >&2; exit 1 ;;
esac
case "$target" in *[!A-Za-z0-9.@_-]*) echo 'SSH target contains unsupported characters' >&2; exit 1 ;; esac
case "$port" in *[!0-9]*|'') echo 'SSH port must be numeric' >&2; exit 1 ;; esac
[ "$port" -ge 1 ] && [ "$port" -le 65535 ] || { echo 'SSH port must be between 1 and 65535' >&2; exit 1; }
test -s "$known_hosts_input"

secrets=/etc/embymedia/secrets
identity=$secrets/nas-transfer-worker
known_hosts=/etc/embymedia/nas-transfer-worker-known-hosts
environment=/etc/embymedia/v2.env
install -o root -g embymedia -m 0750 -d /etc/embymedia "$secrets"
if [ ! -f "$identity" ]; then
  ssh-keygen -q -t ed25519 -N '' -C embymedia-nas-worker -f "$identity"
fi
chown embymedia:embymedia "$identity"
chmod 0600 "$identity"
chown root:embymedia "$identity.pub"
chmod 0640 "$identity.pub"
install -o root -g embymedia -m 0640 "$known_hosts_input" "$known_hosts"

temporary=$(mktemp /etc/embymedia/.v2-env-XXXXXXXX)
if [ -f "$environment" ]; then
  sed '/^EMBYMEDIA_NAS_WORKER_SSH_/d' "$environment" > "$temporary"
fi
printf '%s\n' \
  "EMBYMEDIA_NAS_WORKER_SSH_TARGET=$target" \
  "EMBYMEDIA_NAS_WORKER_SSH_PORT=$port" \
  "EMBYMEDIA_NAS_WORKER_SSH_IDENTITY_FILE=$identity" \
  "EMBYMEDIA_NAS_WORKER_SSH_KNOWN_HOSTS_FILE=$known_hosts" >> "$temporary"
chown root:embymedia "$temporary"
chmod 0640 "$temporary"
mv -f "$temporary" "$environment"

printf '%s\n' "$identity.pub"
