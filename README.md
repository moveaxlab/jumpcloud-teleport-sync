# JumpCloud → Teleport SCIM Sync

Syncs users from a JumpCloud group into Teleport Community Edition as local users.
Designed to run as a Kubernetes CronJob in the same cluster/namespace as Teleport.

## Architecture

```
┌────────────┐    API     ┌──────────────┐   tbot init   ┌─────────────┐
│ JumpCloud  │◄───────────│  CronJob Pod │──────────────►│  Teleport   │
│  (group)   │            │              │   identity    │  Auth Svc   │
└────────────┘            │ init: tbot   │──────────────►│             │
                          │ main: sync   │   Go client   └─────────────┘
                          └──────────────┘
```

## Prerequisites

- Teleport Community Edition running in Kubernetes
- JumpCloud service account (client ID + secret) with read access to users and groups
- Docker registry for the sync image

## Quick Start

### 1. Build and push the image

```bash
docker build -t your-registry/jumpcloud-teleport-sync:latest .
docker push your-registry/jumpcloud-teleport-sync:latest
```

### 2. Install the Helm chart

The chart automatically creates the Teleport bot, role, and join token
via a post-install hook. No manual `tctl` step needed.

```bash
helm install jc-sync ./chart -n teleport \
  --set jumpcloudGroupName="My Teleport Users" \
  --set jumpcloudClientID="your-client-id" \
  --set jumpcloudClientSecret="your-client-secret" \
  --set jumpcloudOrgID="your-org-id" \
  --set image.repository=your-registry/jumpcloud-teleport-sync \
  --set image.tag=latest
```

### 3. Test with dry run

```bash
helm install jc-sync ./chart -n teleport \
  --set jumpcloudGroupName="My Teleport Users" \
  --set jumpcloudClientID="your-client-id" \
  --set jumpcloudClientSecret="your-client-secret" \
  --set jumpcloudOrgID="your-org-id" \
  --set dryRun=true

kubectl create job --from=cronjob/jc-sync-jumpcloud-teleport-sync test-sync -n teleport
kubectl logs -f job/test-sync -n teleport
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
helm install jc-sync ./chart -n teleport \
  --set setup.enabled=false \
  ...
```

Then run `./setup-teleport-bot.sh` manually with `tctl` access.

## Configuration

| Parameter | Description | Default |
|-----------|-------------|---------|
| `jumpcloudGroupName` | JumpCloud group to sync | (required) |
| `jumpcloudClientID` | JumpCloud service account client ID | (required unless existingSecret) |
| `jumpcloudClientSecret` | JumpCloud service account client secret | (required unless existingSecret) |
| `jumpcloudOrgID` | JumpCloud organization ID | (required unless existingSecret) |
| `existingSecret` | Use existing K8s secret | `""` |
| `teleportAddr` | Teleport auth address | `teleport-auth.teleport.svc.cluster.local:3025` |
| `teleportDefaultRoles` | Roles for synced users | `access` |
| `schedule` | Cron schedule | `*/15 * * * *` |
| `dryRun` | Log-only mode | `false` |
| `teleportImage.tag` | Teleport version for tbot | `16` |
| `setup.enabled` | Run automatic bot setup hook | `true` |
| `setup.teleportAuthSelector` | Label selector for auth pod | `app=teleport` |
| `setup.teleportAuthContainer` | Container name with tctl | `teleport` |
| `setup.kubectlImage.tag` | kubectl image version | `1.30` |

## Behavior

- **Creates** Teleport users for new JumpCloud group members (with invite link)
- **Updates** email, name, and roles if changed
- **Locks + deletes** users removed from the JumpCloud group
- **Skips** users not managed by this tool (no `managed-by: jumpcloud-scim-sync` label)
- Suspended/deactivated JumpCloud users are skipped
