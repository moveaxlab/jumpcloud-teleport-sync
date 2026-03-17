#!/usr/bin/env bash
#
# OPTIONAL — Manual fallback for Teleport bot setup.
#
# By default the Helm chart handles this automatically via a post-install hook
# (setup.enabled=true). Use this script only if:
#   - You disabled the hook (--set setup.enabled=false)
#   - You need to debug or recreate resources manually
#   - Your cluster RBAC doesn't allow pods/exec
#
# Requires: tctl access to your Teleport cluster.
#
# Usage:
#   RELEASE_NAME=jumpcloud-teleport-sync NAMESPACE=teleport ./setup-teleport-bot.sh
#
set -euo pipefail

RELEASE_NAME="${RELEASE_NAME:-jumpcloud-teleport-sync}"
NAMESPACE="${NAMESPACE:-teleport}"
BOT_NAME="${RELEASE_NAME}-bot"
ROLE_NAME="${RELEASE_NAME}-bot-role"
SA_NAME="${RELEASE_NAME}"

echo "==> Creating bot role: ${ROLE_NAME}"
cat <<EOF | tctl create -f
kind: role
version: v7
metadata:
  name: ${ROLE_NAME}
spec:
  allow:
    rules:
      - resources: [user]
        verbs: [list, read, create, update, delete]
      - resources: [token]
        verbs: [list, read, create]
      - resources: [lock]
        verbs: [list, read, create, update]
      - resources: [reset_password_token]
        verbs: [create]
EOF

echo "==> Creating bot join token: ${BOT_NAME}"
cat <<EOF | tctl create -f
kind: token
version: v2
metadata:
  name: ${BOT_NAME}
spec:
  roles: [Bot]
  join_method: kubernetes
  bot_name: ${BOT_NAME}
  kubernetes:
    type: static
    allow:
      - service_account: "${NAMESPACE}:${SA_NAME}"
EOF

echo "==> Creating Machine ID bot: ${BOT_NAME}"
tctl bots add "${BOT_NAME}" \
  --roles="${ROLE_NAME}" \
  --token="${BOT_NAME}" \
  --logins=""
