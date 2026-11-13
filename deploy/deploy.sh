#!/usr/bin/env bash
#
# Deploy ignitenet-proxy to a host over SSH: installs the binary, the systemd
# unit and the secret env file, then (re)starts and health-checks the service.
#
# Usage:
#   DEPLOY_HOST=root@your-host IGN_PASSWORD='...' ./deploy/deploy.sh [path-to-binary]
#
# Environment:
#   DEPLOY_HOST    SSH target (required, e.g. root@host)
#   DEPLOY_PORT    target HTTP port  (default: 8881)
#   IGN_USERNAME   device username   (default: root)
#   IGN_PASSWORD   device password   (required)
#   SSH_OPTS       extra ssh/scp options (optional)
set -euo pipefail

BIN="${1:-ignitenet-proxy}"
DEPLOY_HOST="${DEPLOY_HOST:-}"
DEPLOY_PORT="${DEPLOY_PORT:-8881}"
IGN_USERNAME="${IGN_USERNAME:-root}"
SSH_OPTS="${SSH_OPTS:-}"

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
UNIT="${SCRIPT_DIR}/ignitenet-proxy.service"

[[ -z "$DEPLOY_HOST" ]]      && { echo "ERROR: DEPLOY_HOST is not set" >&2; exit 1; }
[[ -z "${IGN_PASSWORD:-}" ]] && { echo "ERROR: IGN_PASSWORD is not set" >&2; exit 1; }
[[ ! -f "$BIN" ]]           && { echo "ERROR: binary not found: $BIN" >&2; exit 1; }

# shellcheck disable=SC2086
SSH="ssh $SSH_OPTS -o StrictHostKeyChecking=accept-new"
# shellcheck disable=SC2086
SCP="scp $SSH_OPTS -o StrictHostKeyChecking=accept-new"

echo ">> copying artifacts to ${DEPLOY_HOST}"
$SCP "$BIN" "${DEPLOY_HOST}:/tmp/ignitenet-proxy.new"
$SCP "$UNIT" "${DEPLOY_HOST}:/tmp/ignitenet-proxy.service"

echo ">> installing on ${DEPLOY_HOST}"
$SSH "$DEPLOY_HOST" "IGN_USERNAME='${IGN_USERNAME}' IGN_PASSWORD='${IGN_PASSWORD}' DEPLOY_PORT='${DEPLOY_PORT}' bash -s" <<'REMOTE'
set -euo pipefail
install -m 0755 /tmp/ignitenet-proxy.new /usr/local/bin/ignitenet-proxy
rm -f /tmp/ignitenet-proxy.new
install -m 0644 /tmp/ignitenet-proxy.service /etc/systemd/system/ignitenet-proxy.service
rm -f /tmp/ignitenet-proxy.service

umask 077
{
  printf 'USERNAME=%s\n' "$IGN_USERNAME"
  printf 'PASSWORD=%s\n' "$IGN_PASSWORD"
} > /etc/ignitenet-proxy.env
chmod 600 /etc/ignitenet-proxy.env

systemctl daemon-reload
systemctl enable --now ignitenet-proxy.service
systemctl restart ignitenet-proxy.service

for i in $(seq 1 10); do
  if curl -fsS "http://127.0.0.1:${DEPLOY_PORT}/healthz" >/dev/null 2>&1; then
    echo ">> healthz OK on :${DEPLOY_PORT}"
    exit 0
  fi
  sleep 1
done
echo "ERROR: service did not become healthy" >&2
journalctl -u ignitenet-proxy.service --no-pager --lines=30 || true
exit 1
REMOTE

echo ">> deploy complete"
