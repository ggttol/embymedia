#!/bin/sh
set -eu

snapshot=${1:?snapshot id required}
target=${2:?isolated restore target required}
case "$target" in
  /srv/embymedia/restore-*) ;;
  *) echo 'restore target must be /srv/embymedia/restore-*' >&2; exit 1 ;;
esac
case "${target#/srv/embymedia/restore-}" in
  ''|*[!A-Za-z0-9._-]*) echo 'restore target must use a single directory name' >&2; exit 1 ;;
esac
[ ! -e "$target" ] && [ ! -L "$target" ] || { echo 'restore target already exists' >&2; exit 1; }
mkdir -m 0700 "$target"
RESTIC_PASSWORD_FILE=/etc/embymedia/secrets/restic-local-password \
  restic --repo /srv/embymedia/backups/restic-local restore "$snapshot" --target "$target"
chmod 0700 "$target"
online_snapshot="$target/srv/embymedia/backups/.online-snapshot"
if [ -d "$online_snapshot" ]; then
  test -s "$online_snapshot/.embymedia-online-backup.json"
  cp -a "$online_snapshot"/. "$target"/
  rm -f "$target/.embymedia-online-backup.json"
  rm -rf "$online_snapshot"
fi

database="$target/srv/embymedia/data/embymedia.db"
test -r "$database"
test -d "$target/srv/embymedia/data/emby/config"
test -d "$target/srv/embymedia/data/clouddrive/config"
test -d "$target/srv/embymedia/data/strm"
test -d "$target/srv/embymedia/data/authelia"
login_config="$target/srv/embymedia/data/auth/http-login.json"
test -s "$login_config"
/usr/bin/python3 - /opt/embymedia-v2/current/deploy/scripts/http-login.py "$target" "$database" "$login_config" <<'PY'
import base64
import importlib.util
import json
from pathlib import Path
import sys

module_path, target, database, login_path = sys.argv[1:]
root = Path(target).resolve(strict=True)
for path in (database, login_path):
    Path(path).resolve(strict=True).relative_to(root)
spec = importlib.util.spec_from_file_location('restored_http_login', module_path)
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)
config = json.loads(Path(login_path).read_text())
if config.get('version') == 2 and not config.get('users'):
    raise SystemExit('restored browser identity list is empty')
state = module.LoginState(login_path)
if len(state.session_secret) < 32:
    raise SystemExit('restored browser session secret is invalid')
if isinstance(config.get('users'), list) and len(config['users']) != len(state.users):
    raise SystemExit('restored browser identities contain duplicate usernames')
for user in state.users.values():
    salt = str(user['passwordSalt'])
    digest = str(user['passwordHash'])
    salt_bytes = base64.b64decode(salt + '=' * (-len(salt) % 4), altchars=b'-_', validate=True)
    digest_bytes = base64.b64decode(digest + '=' * (-len(digest) % 4), altchars=b'-_', validate=True)
    if len(salt_bytes) < 16 or len(digest_bytes) != 32 or int(user['iterations']) <= 0:
        raise SystemExit('restored browser password credentials are invalid')
Path(login_path).chmod(0o600)
print('restored browser identities validated')
PY

# The check command opens the isolated database, runs monotonic migrations, and starts no workers.
/opt/embymedia-v2/current/bin/embymedia -check-db -db "$database"
test -s "$database"
