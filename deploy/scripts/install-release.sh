#!/bin/sh
set -eu

source_tree=${1:?source tree required}
release_id=${2:-$(date -u +%Y%m%dT%H%M%SZ)}
release=/opt/embymedia/releases/$release_id

[ "$(id -u)" -eq 0 ] || { echo 'run as root' >&2; exit 1; }
case "$release_id" in
  *[!A-Za-z0-9._-]*|'') echo 'invalid release id' >&2; exit 1 ;;
esac
source_tree=$(realpath "$source_tree")
test -r "$source_tree/package.json"
test -r "$source_tree/pnpm-lock.yaml"
test ! -e "$release"

mkdir -p "$release"
trap 'rm -rf "$release"' EXIT INT TERM
commit=$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["commit"])' "$source_tree/deploy/upstream-lock.json")
rsync -a --delete \
  --exclude=.git/ \
  --exclude=node_modules/ \
  --exclude=__pycache__/ \
  --exclude=coverage/ \
  --exclude=.turbo/ \
  --exclude=target/ \
  --exclude=lib/ \
  --exclude=.dsh-build/ \
  --exclude=.typecheck/ \
  --exclude=*.tsbuildinfo \
  "$source_tree/" "$release/"

(
  cd "$release"
  CI=1 /usr/local/bin/pnpm install --frozen-lockfile
  DSH_CLIENT_COMMIT_HASH="$commit" /usr/local/bin/pnpm build
  /usr/local/bin/pnpm --filter @embymedia/migrate bundle
  /usr/local/bin/pnpm --filter @embymedia/control-helper bundle
)
chown -R root:root "$release"
find "$release" -type d -exec chmod go-w {} +
find "$release" -type f -exec chmod go-w {} +
chown embymedia:embymedia "$release/migration"
chmod 0750 "$release/migration"
ln -sfn "$release" /opt/embymedia/current.next
mv -Tf /opt/embymedia/current.next /opt/embymedia/current
trap - EXIT INT TERM
printf '%s\n' "$release"
