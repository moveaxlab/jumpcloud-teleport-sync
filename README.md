# JumpCloud → Teleport SCIM Sync

Syncs users from a JumpCloud group into Teleport Community Edition as local users.
Designed to run as a Kubernetes Deployment with a tbot sidecar in the same cluster/namespace as Teleport.

## Architecture

```
┌────────────┐    API     ┌───────────────┐  tbot daemon  ┌─────────────┐
│ JumpCloud  │◄───────────│  Deployment   │──────────────►│  Teleport   │
│  (group)   │            │               │   identity    │  Auth Svc   │
└────────────┘            │ sidecar: tbot │──────────────►│             │
                          │ main: sync    │   Go client   └─────────────┘
                          └───────────────┘
```

The tbot sidecar runs continuously, renewing certificates automatically.
The sync container waits for the identity file, runs an initial sync at startup,
then follows the configured cron schedule internally.

## Prerequisites

- Teleport Community Edition running in Kubernetes
- JumpCloud service account (client ID + secret) with read access to users and groups: https://console.jumpcloud.com/#/settings/service-accounts

## Quick Start

### Install the Helm chart

The chart automatically creates the Teleport bot, role, and join token
via a post-install hook. No manual `tctl` step needed.

```bash
helm repo add jumpcloud-teleport-sync https://moveaxlab.github.io/jumpcloud-teleport-sync/

helm install jumpcloud-teleport-sync jumpcloud-teleport-sync/jumpcloud-teleport-sync -n teleport \
  --set jumpcloud.groupName="My Teleport Users" \
  --set jumpcloud.clientID="your-client-id" \
  --set jumpcloud.clientSecret="your-client-secret" \
  --set jumpcloud.orgID="your-org-id" \
  --set image.repository=your-registry/jumpcloud-teleport-sync \
  --set image.tag=latest
```

### Test with dry run

```bash
helm install jumpcloud-teleport-sync jumpcloud-teleport-sync/jumpcloud-teleport-sync -n teleport \
  --set jumpcloud.groupName="My Teleport Users" \
  --set jumpcloud.clientID="your-client-id" \
  --set jumpcloud.clientSecret="your-client-secret" \
  --set jumpcloud.orgID="your-org-id" \
  --set dryRun=true

kubectl logs -f deployment/jumpcloud-teleport-sync -c sync -n teleport
```

## Setup Hook (ArgoCD compatible)

The chart includes a post-install/post-upgrade hook that:

1. Finds the running Teleport auth pod via label selector
2. Runs `tctl create -f` to upsert the bot role and join token (idempotent)
3. Checks if the bot exists before creating it (idempotent)

This means ArgoCD can re-sync freely without errors. The hook resources
use `before-hook-creation` delete policy, so stale Jobs are cleaned up.

To disable the hook and manage setup manually:

```bash
helm install jumpcloud-teleport-sync jumpcloud-teleport-sync/jumpcloud-teleport-sync -n teleport \
  --set setup.enabled=false \
  ...
```

Then run `./setup-teleport-bot.sh` manually with `tctl` access.

## Configuration

| Parameter | Description | Default |
|-----------|-------------|---------|
| `jumpcloud.groupName` | JumpCloud group to sync | (required) |
| `jumpcloud.clientID` | JumpCloud service account client ID | (required unless `jumpcloud.existingSecret`) |
| `jumpcloud.clientSecret` | JumpCloud service account client secret | (required unless `jumpcloud.existingSecret`) |
| `jumpcloud.orgID` | JumpCloud organization ID | (required unless `jumpcloud.existingSecret`) |
| `jumpcloud.existingSecret` | Use existing K8s secret for JumpCloud credentials | `""` |
| `teleportAddr` | Teleport auth address | `teleport-auth.teleport.svc.cluster.local:3025` |
| `teleportDefaultRoles` | Roles for synced users | `access` |
| `schedule` | Internal cron schedule | `*/15 * * * *` |
| `dryRun` | Log-only mode | `false` |
| `teleportImage.tag` | Teleport version for tbot sidecar | `18` |
| `tbotResources` | Resources for the tbot sidecar | `{requests: {cpu: 25m, memory: 32Mi}, limits: {cpu: 100m, memory: 64Mi}}` |
| `setup.enabled` | Run automatic bot setup hook | `true` |
| `setup.teleportAuthSelector` | Label selector for auth pod | `app=teleport` |
| `setup.teleportAuthContainer` | Container name with tctl | `teleport` |
| `setup.kubectlImage.tag` | kubectl image version | `1.35` |

## Behavior

- **Creates** Teleport users for new JumpCloud group members (with invite link)
- **Updates** email, name, and roles if changed
- **Locks + deletes** users removed from the JumpCloud group
- **Skips** users not managed by this tool (no `managed-by: jumpcloud-scim-sync` label)
- Suspended/deactivated JumpCloud users are skipped
- The sync container waits for tbot to generate the identity file before starting
- On SIGTERM/SIGINT the process shuts down gracefully, waiting for any in-flight sync to complete
