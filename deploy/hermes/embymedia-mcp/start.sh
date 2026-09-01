#!/usr/bin/env bash
set -euo pipefail
export EMBYMEDIA_DATABASE_URL="$(sudo -n grep '^EMBYMEDIA_DATABASE_URL=' /etc/embymedia/dsh.env | cut -d= -f2-)"
export EMBYMEDIA_MCP_SESSION_ID="${EMBYMEDIA_MCP_SESSION_ID:-hermes-weixin}"
exec /usr/local/bin/node /opt/embymedia/mcp-server/src/index.js
