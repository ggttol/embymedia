#!/bin/sh
set -eu
umask 077

source_tree=${1:?source tree required}
release_id=${2:-$(date -u +%Y%m%dT%H%M%SZ)}
force=${EMBYMEDIA_DEPLOY_FORCE:-0}
case "$force" in 0|1) ;; *) echo 'EMBYMEDIA_DEPLOY_FORCE must be 0 or 1' >&2; exit 1 ;; esac

[ "$(id -u)" -eq 0 ] || { echo 'run as root' >&2; exit 1; }
case "$release_id" in
  *[!A-Za-z0-9._-]*|''|.|..) echo 'invalid release id' >&2; exit 1 ;;
esac
exec 9>/run/lock/embymedia-backup.lock
flock -n 9 || { echo 'another deployment or backup is running' >&2; exit 1; }
source_tree=$(realpath "$source_tree")
install -o root -g root -m 0755 -d /opt/embymedia-v2 /opt/embymedia-v2/releases
release=$(realpath /opt/embymedia-v2/releases)/$release_id
binary="$source_tree/bin/embymedia-linux-amd64"
checksum="$binary.sha256"
database=/srv/embymedia/data/embymedia.db
current=/opt/embymedia-v2/current
previous=$(readlink -f "$current" 2>/dev/null || true)
if [ -e "$current" ] || [ -L "$current" ]; then
  [ -L "$current" ] && [ -x "$previous/bin/embymedia" ] || { echo 'current must reference a working release' >&2; exit 1; }
else
  previous=
fi
test -x "$binary"
test -r "$checksum"
test ! -e "$release"
test ! -L "$release"
(cd "$(dirname "$binary")" && sha256sum -c "$(basename "$checksum")")

state=$(mktemp -d /opt/embymedia-v2/.install-XXXXXXXX)
changed=0
activated=0
committed=0
units='embymedia-stack.service embymedia-http-login.service embymedia-v2.service caddy.service embymedia-backup.timer embymedia-clouddrive-recovery.service embymedia-clouddrive-recovery.timer embymedia-dsh.service embymedia-control-helper.service'

wait_http() {
  attempts=0
  while [ "$attempts" -lt 30 ]; do
    if curl -fsS --max-time 2 "$@" >/dev/null; then return 0; fi
    attempts=$((attempts + 1))
    sleep 1
  done
  return 1
}

wait_task_queue_idle() {
  attempts=0
  announced=0
  while [ "$attempts" -lt 720 ]; do
    active=$(curl -fsS --max-time 2 -H 'Remote-User: release-probe' 'http://127.0.0.1:3080/api/v1/async-tasks?status=running')
    case "$active" in
      *'"tasks":[]'*) return 0 ;;
      *'"id":'*)
        if [ "$announced" -eq 0 ]; then
          echo 'waiting for active EmbyMedia task before deployment'
          announced=1
        fi
        attempts=$((attempts + 1))
        sleep 5
        ;;
      *)
        echo 'active task query returned an invalid response; deployment left the running release unchanged' >&2
        return 1
        ;;
    esac
  done
  echo 'active EmbyMedia task did not finish within one hour; deployment left the running release unchanged' >&2
  return 1
}

finish() {
  status=$?
  trap - EXIT
  trap '' INT TERM HUP
  set +e
  recovery_failed=0
  if [ -n "${secret_input:-}" ]; then rm -f "$secret_input"; fi
  if [ -n "${webhook_config_temp:-}" ]; then rm -f "$webhook_config_temp"; fi
  if [ "$committed" -eq 0 ] && [ "$changed" -eq 1 ]; then
    # Database migrations and identity changes are never rolled back by copying old data.
    for unit in $units; do
      if ! systemctl cat "$unit" >/dev/null 2>&1; then continue; fi
      if [ ! -f "$state/$unit.enabled" ]; then
        systemctl disable "$unit" || recovery_failed=1
      fi
      case "$unit" in
        embymedia-http-login.service|embymedia-v2.service) systemctl stop "$unit" || recovery_failed=1 ;;
        *) if [ ! -f "$state/$unit.active" ]; then systemctl stop "$unit" || recovery_failed=1; fi ;;
      esac
    done
    if [ "$activated" -eq 1 ]; then
      if [ -n "$previous" ]; then
        ln -sfn "$previous" "$current.next" && mv -Tf "$current.next" "$current" || recovery_failed=1
      else
        rm -f "$current" || recovery_failed=1
      fi
    fi
    while IFS= read -r destination; do
      rm -f "$destination" || recovery_failed=1
      if [ -e "$state/files$destination" ] || [ -L "$state/files$destination" ]; then
        cp -a "$state/files$destination" "$destination" || recovery_failed=1
      fi
    done < "$state/configs"
    systemctl daemon-reload || recovery_failed=1
    for unit in $units; do
      if [ -f "$state/$unit.enabled" ]; then
        systemctl enable "$unit" || recovery_failed=1
      fi
      if [ -f "$state/$unit.active" ]; then
        if [ "$unit" = caddy.service ]; then
          systemctl reload "$unit" || recovery_failed=1
        else
          systemctl restart "$unit" || recovery_failed=1
        fi
        systemctl is-active --quiet "$unit" || recovery_failed=1
      fi
    done
    if [ -f "$state/embymedia-http-login.service.active" ]; then
      wait_http http://127.0.0.1:9092/health || recovery_failed=1
    fi
    if [ -f "$state/embymedia-v2.service.active" ]; then
      wait_http -H 'Remote-User: release-probe' http://127.0.0.1:3080/api/v1/openapi.json || recovery_failed=1
    fi
  fi
  if [ "$committed" -eq 0 ]; then
    status=1
    # A failed symlink rollback must not turn the active release into a dangling link.
    if active=$(readlink -f "$current" 2>/dev/null); then
      if [ "$active" != "$release" ]; then rm -rf "$release"; fi
    elif [ ! -e "$current" ] && [ ! -L "$current" ]; then
      rm -rf "$release"
    fi
    if [ "$recovery_failed" -eq 0 ]; then rm -f "$current.next"; fi
  fi
  if [ "$recovery_failed" -eq 0 ]; then
    rm -rf "$state"
  else
    echo "deployment recovery failed; saved configurations retained at $state" >&2
    status=1
  fi
  exit "$status"
}
trap finish EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
trap 'exit 129' HUP

mkdir -p "$release/bin"
install -o root -g root -m 0755 "$binary" "$release/bin/embymedia"
cp -R "$source_tree/deploy" "$release/deploy"
chown -R root:root "$release"
chmod 0755 "$release" "$release/bin" "$release/bin/embymedia"
# Runtime services traverse the release tree to read their scripts and compose file.
chmod -R a+rX "$release/deploy"

: > "$state/configs"
for file in "$release"/deploy/systemd/*.service "$release"/deploy/systemd/*.timer; do
  printf '/etc/systemd/system/%s\n' "$(basename "$file")" >> "$state/configs"
done
printf '%s\n' /etc/caddy/Caddyfile /etc/systemd/system/caddy.service.d/embymedia-login.conf /home/gaotao/.hermes/skills/embymedia-v2-operator/SKILL.md /srv/embymedia/data/clouddrive/config/webhooks/webhook.toml >> "$state/configs"
while IFS= read -r destination; do
  if [ -e "$destination" ] || [ -L "$destination" ]; then
    mkdir -p "$state/files$(dirname "$destination")"
    cp -a "$destination" "$state/files$destination"
  fi
done < "$state/configs"
for unit in $units; do
  if systemctl is-active --quiet "$unit"; then touch "$state/$unit.active"; fi
  if systemctl is-enabled --quiet "$unit"; then touch "$state/$unit.enabled"; fi
done

if [ -f "$state/embymedia-v2.service.active" ] && [ "$force" -ne 1 ]; then wait_task_queue_idle; fi
changed=1
if [ -f "$state/embymedia-v2.service.active" ]; then systemctl stop embymedia-v2.service; fi
systemctl disable --now embymedia-dsh.service embymedia-control-helper.service 2>/dev/null || true
install -o embymedia -g embymedia -m 0750 -d /srv/embymedia /srv/embymedia/data
for file in "$database" "$database-wal" "$database-shm" "$database-journal" "$database.owner.lock"; do
  if [ -e "$file" ]; then
    chown embymedia:embymedia "$file"
    chmod 0600 "$file"
  fi
done
runuser -u embymedia -- "$release/bin/embymedia" -check-db -db "$database"
# The service account gets a private temporary copy, not access to the secrets directory.
secret_input=$(mktemp /srv/embymedia/data/.webhook-secret-XXXXXXXX)
install -o embymedia -g embymedia -m 0600 /etc/embymedia/secrets/clouddrive-webhook-secret "$secret_input"
webhook_config_dir=/srv/embymedia/data/clouddrive/config/webhooks
webhook_config=$webhook_config_dir/webhook.toml
install -o root -g root -m 0700 -d "$webhook_config_dir"
webhook_config_temp=$(mktemp "$webhook_config_dir/.webhook-XXXXXXXX")
{
  printf '%s\n' '[file_system_watcher]' 'enabled = true'
  printf '%s' 'url = "http://host.docker.internal/hooks/clouddrive2?key='
  tr -d '\r\n' < "$secret_input"
  printf '%s\n' '"' 'method = "POST"'
} > "$webhook_config_temp"
chown root:root "$webhook_config_temp"
chmod 0600 "$webhook_config_temp"
if [ -f "$webhook_config" ] && cmp -s "$webhook_config_temp" "$webhook_config"; then
  rm -f "$webhook_config_temp"
else
  mv -f "$webhook_config_temp" "$webhook_config"
fi
webhook_config_temp=
runuser -u embymedia -- "$release/bin/embymedia" -bootstrap-webhook-secret-file "$secret_input" -db "$database"
rm -f "$secret_input"
secret_input=
login_config=/srv/embymedia/data/auth/http-login.json
install -o embymedia -g embymedia -m 0700 -d /srv/embymedia/data/auth
if [ ! -s "$login_config" ]; then
  if [ -s /etc/embymedia/http-login.json ]; then
    install -o embymedia -g embymedia -m 0600 /etc/embymedia/http-login.json "$login_config"
  else
    /usr/bin/python3 "$release/deploy/scripts/init-http-login.py" --password-file /etc/embymedia/secrets/admin-bootstrap-password --output "$login_config"
  fi
fi
chown embymedia:embymedia "$login_config"
chmod 0600 "$login_config"
ln -sfn "$release" "$current.next"
activated=1
mv -Tf "$current.next" "$current"

install -o root -g root -m 0644 "$release"/deploy/systemd/*.service "$release"/deploy/systemd/*.timer /etc/systemd/system/
install -o root -g root -m 0644 "$release/deploy/caddy/Caddyfile" /etc/caddy/Caddyfile
install -o root -g root -m 0755 -d /etc/systemd/system/caddy.service.d
install -o root -g root -m 0644 "$release/deploy/caddy/embymedia-login.conf" /etc/systemd/system/caddy.service.d/embymedia-login.conf
skill_source="$release/deploy/hermes/skills/embymedia-v2-operator/SKILL.md"
install -o gaotao -g gaotao -m 0755 -d /home/gaotao/.hermes/skills/embymedia-v2-operator
install -o gaotao -g gaotao -m 0644 "$skill_source" /home/gaotao/.hermes/skills/embymedia-v2-operator/SKILL.md

systemctl daemon-reload
systemctl enable --now embymedia-stack.service embymedia-backup.timer embymedia-clouddrive-recovery.timer
systemctl enable --now embymedia-http-login.service
systemctl enable embymedia-v2.service
systemctl restart embymedia-http-login.service
systemctl start embymedia-v2.service
wait_http http://127.0.0.1:9092/health
wait_http -H 'Remote-User: release-probe' http://127.0.0.1:3080/api/v1/openapi.json
if systemctl is-active --quiet caddy.service; then
  systemctl reload caddy.service
else
  systemctl start caddy.service
fi
systemctl is-active --quiet caddy.service
committed=1
printf '%s\n' "$release"
