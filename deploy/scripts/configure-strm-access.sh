#!/bin/sh
set -eu

strm_root=${1:?STRM root required}
stack_env=${2:?stack environment required}
command -v setfacl >/dev/null || { echo 'install the acl package before configuring STRM access' >&2; exit 1; }
[ ! -L "$strm_root" ] || { echo 'STRM root must not be a symbolic link' >&2; exit 1; }
strm_gid=$(id -g embymedia)
install -o embymedia -g embymedia -m 2775 -d "$strm_root"
# Only generated STRM directories share deletion rights; private service data stays unchanged.
find "$strm_root" -xdev -type d -exec chown embymedia:embymedia {} + -exec chmod 2775 {} +
find "$strm_root" -xdev -type d -exec setfacl -m d:u::rwx,d:g::rwx,d:o::rx {} +

temporary=$(mktemp "${stack_env}.strm-XXXXXXXX")
trap 'rm -f "$temporary"' EXIT HUP INT TERM
sed '/^STRM_GID=/d' "$stack_env" > "$temporary"
printf '\nSTRM_GID=%s\n' "$strm_gid" >> "$temporary"
chown root:root "$temporary"
chmod 0640 "$temporary"
mv -f "$temporary" "$stack_env"
