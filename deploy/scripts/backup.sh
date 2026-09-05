#!/bin/sh
set -eu

exec 9>/run/lock/embymedia-backup.lock
flock -n 9 || exit 0

local_repo=/srv/embymedia/backups/restic-local
local_password=/etc/embymedia/secrets/restic-local-password
login_config=/srv/embymedia/data/auth/http-login.json
v2_was_active=0
stack_was_active=0
if systemctl is-active --quiet embymedia-v2.service; then v2_was_active=1; fi
if systemctl is-active --quiet embymedia-stack.service; then stack_was_active=1; fi

restore_services() {
  if [ "$stack_was_active" -eq 1 ]; then
    systemctl start embymedia-stack.service
    stack_was_active=0
  fi
  if [ "$v2_was_active" -eq 1 ]; then
    systemctl start embymedia-v2.service
    v2_was_active=0
  fi
}
trap restore_services EXIT INT TERM
test -r "$local_password"
test -s "$login_config"

if [ "$v2_was_active" -eq 1 ]; then systemctl stop embymedia-v2.service; fi
if [ "$stack_was_active" -eq 1 ]; then systemctl stop embymedia-stack.service; fi
if [ ! -d "$local_repo" ]; then
  RESTIC_PASSWORD_FILE="$local_password" restic init --repo "$local_repo"
fi
RESTIC_PASSWORD_FILE="$local_password" restic --repo "$local_repo" backup \
  /srv/embymedia/data/embymedia.db \
  "$login_config" \
  /srv/embymedia/data/emby/config \
  /srv/embymedia/data/clouddrive/config \
  /srv/embymedia/data/strm \
  /srv/embymedia/data/authelia \
  /etc/embymedia/authelia \
  /etc/embymedia/secrets/authelia-users.yml \
  --tag embymedia-v2-daily
RESTIC_PASSWORD_FILE="$local_password" restic --repo "$local_repo" forget --tag embymedia-v2-daily --group-by host,tags --keep-daily 14 --prune

restore_services
trap - EXIT INT TERM
