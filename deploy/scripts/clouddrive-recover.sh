#!/bin/sh
set -eu

mode=${1:-monitor}
mount_path=/srv/embymedia/data/clouddrive/CloudNAS/CloudDrive
canary=$mount_path/.embymedia-health-canary
failure_file=/run/embymedia-clouddrive.failures
attempt_file=/run/embymedia-clouddrive.last-attempt
compose_file=/opt/embymedia-v2/current/deploy/compose.yml
env_file=/etc/embymedia/stack.env

compose() {
  docker compose --env-file "$env_file" -f "$compose_file" "$@"
}

mounted() {
  findmnt --mountpoint "$mount_path" >/dev/null 2>&1
}

healthy_mount() {
  timeout 8 stat "$canary" >/dev/null 2>&1
}

cleanup_mount() {
  if mounted; then
    umount -l "$mount_path"
  fi
}

case "$mode" in
  cleanup)
    cleanup_mount
    exit 0
    ;;
  stop-stack)
    compose stop
    cleanup_mount
    exit 0
    ;;
  prepare)
    if mounted && ! healthy_mount; then cleanup_mount; fi
    exit 0
    ;;
  monitor) ;;
  *) echo "usage: $0 [monitor|prepare|cleanup|stop-stack]" >&2; exit 2 ;;
esac

exec 9>/run/lock/embymedia-backup.lock
flock -n 9 || exit 0
systemctl is-active --quiet docker.service || { echo 'CloudDrive recovery skipped: Docker is inactive' >&2; exit 1; }
systemctl is-active --quiet embymedia-stack.service || exit 0

container=$(compose ps -q clouddrive2)
health=missing
if [ -n "$container" ]; then
  health=$(docker inspect --format '{{if .State.Health}}{{.State.Health.Status}}{{else}}{{.State.Status}}{{end}}' "$container" 2>/dev/null || printf missing)
fi
if [ "$health" = healthy ] && healthy_mount; then
  rm -f "$failure_file"
  exit 0
fi

failures=0
if [ -r "$failure_file" ]; then read -r failures < "$failure_file" || failures=0; fi
failures=$((failures + 1))
printf '%s\n' "$failures" > "$failure_file"
if [ "$health" != unhealthy ] && [ "$failures" -lt 3 ]; then
  echo "CloudDrive health failure $failures/3: container=$health" >&2
  exit 0
fi

now=$(date +%s)
last=0
if [ -r "$attempt_file" ]; then read -r last < "$attempt_file" || last=0; fi
if [ $((now - last)) -lt 900 ]; then
  echo "CloudDrive recovery cooling down: container=$health failures=$failures" >&2
  exit 0
fi
printf '%s\n' "$now" > "$attempt_file"

echo "Recovering CloudDrive: container=$health failures=$failures" >&2
v2_was_active=0
if systemctl is-active --quiet embymedia-v2.service; then
  v2_was_active=1
  systemctl stop embymedia-v2.service
fi
restore_v2() {
  if [ "$v2_was_active" -eq 1 ]; then
    systemctl start embymedia-v2.service
    v2_was_active=0
  fi
}
trap restore_v2 EXIT INT TERM HUP

compose stop -t 20 emby
compose stop -t 20 clouddrive2
cleanup_mount
timeout 300 docker compose --env-file "$env_file" -f "$compose_file" up -d --wait clouddrive2
healthy_mount || { echo 'CloudDrive mount stayed unreadable after container recovery' >&2; exit 1; }
timeout 300 docker compose --env-file "$env_file" -f "$compose_file" up -d --wait emby
compose exec -T emby timeout 8 stat /media/.embymedia-health-canary >/dev/null
restore_v2
trap - EXIT INT TERM HUP
rm -f "$failure_file"
echo 'CloudDrive mount and Emby bind mount recovered' >&2
