# Hub Configuration

## profiles.json

Resource profiles, storage mounts, and idle policies for Analytics Hub workspaces. Re-read on every spawn — no Hub restart needed.

### Root Fields

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `default_profile` | string | yes | Profile used when no OIDC claim matches |
| `hub_limits` | object | no | Global resource budget for the Hub |
| `profiles` | object | yes | Named resource profiles |
| `role_map` | object | no | Hugr role → profile name fallback mapping |
| `storage` | object | no | Shared storage definitions and credentials |
| `idle_policy` | object | no | Global idle culler settings |

### hub_limits

Global limits across all workspaces. Spawn rejected if any exceeded.

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `max_memory` | string | unlimited | Total memory (e.g., `"128G"`) |
| `max_cpu` | float | unlimited | Total CPU cores |
| `max_servers` | int | unlimited | Max concurrent workspace containers |

### profiles.{name}

Each profile defines resource allocation, timeouts, budgets, and storage for a workspace tier.

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `display_name` | string | **required** | Shown in profile selection UI |
| `description` | string | — | Details in selection UI |
| `value` | string | profile name | Match against OIDC claim value. If null — match by profile name |
| `rank` | int | **required** | Profile tier for ordering (higher = more resources) |
| `mem_limit` | string | unlimited | Container memory limit (e.g., `"4G"`, `"512M"`) |
| `mem_guarantee` | string | 0 | Reserved memory |
| `cpu_limit` | float | unlimited | Max CPU cores |
| `cpu_guarantee` | float | 0 | Reserved CPU cores |
| `swap_factor` | float | 1.5 | `memswap_limit = mem_limit × swap_factor`. Set `0` to disable swap |
| `gpu` | int | — | Number of GPUs (requires nvidia-docker) |
| `image` | string | default | Custom container image for this profile |
| `user_max_servers` | int | unlimited | Max concurrent servers per user |
| `user_max_memory` | string | unlimited | Total memory budget per user |
| `user_max_cpu` | float | unlimited | Total CPU budget per user |
| `idle_timeout` | int | global | Seconds idle before workspace is stopped |
| `max_age` | int | global | Max workspace lifetime in seconds |
| `volumes` | object | — | Storage volumes to mount (see below) |

### profiles.{name}.volumes.{vol_name}

Per-profile storage mount. References a volume defined in `storage.volumes`.

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `mount` | string | **required** | Mount path inside container (e.g., `"/home/jovyan/s3/data"`) |
| `mode` | string | `"ro"` | `"ro"` (read-only) or `"rw"` (read-write) |

### role_map

Fallback mapping when OIDC profile claim doesn't match. Maps Hugr role (from `HUGR_ROLE_CLAIM`) to profile name.

```json
{
  "role_map": {
    "admin": "large",
    "analyst": "medium",
    "viewer": "small"
  }
}
```

### storage.volumes.{name}

Named storage definitions. Referenced by profile `volumes`.

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `type` | string | yes | `"local"`, `"nfs"`, `"s3"`, `"azure"`, `"gcs"` |
| `auth` | string | no | `"shared"` (default) or `"user_token"` |

**Type: local** (bind mount from host)

| Field | Type | Description |
|-------|------|-------------|
| `path` | string | Absolute host path |

**Type: nfs** (Docker NFS volume driver)

| Field | Type | Description |
|-------|------|-------------|
| `server` | string | NFS server address |
| `path` | string | NFS export path |
| `options` | string | Mount options (e.g., `"ro,nfsvers=4.1"`) |

**Type: s3** (FUSE mount via s3fs)

| Field | Type | Description |
|-------|------|-------------|
| `bucket` | string | S3 bucket name |
| `credentials_ref` | string | Reference to `storage.credentials` entry |
| `region` | string | AWS region (optional for MinIO) |
| `read_only` | bool | Default mount mode |
| `required_scope` | string | OIDC scope needed for `user_token` auth |

**Type: azure** (FUSE mount via blobfuse2 — future)

| Field | Type | Description |
|-------|------|-------------|
| `account` | string | Azure storage account |
| `container` | string | Blob container name |
| `credentials_ref` | string | Reference to credentials |
| `required_scope` | string | OIDC scope for user_token auth |

**Type: gcs** (FUSE mount via gcsfuse — future)

| Field | Type | Description |
|-------|------|-------------|
| `bucket` | string | GCS bucket name |
| `credentials_ref` | string | Reference to credentials |
| `required_scope` | string | OIDC scope for user_token auth |

### storage.credentials.{name}

Named credential sets. Values can be plain text or `${secret:KEY}` references.

```json
{
  "credentials": {
    "minio": {
      "access_key_id": "${secret:MINIO_USER}",
      "secret_access_key": "${secret:MINIO_PASSWORD}",
      "endpoint_url": "${secret:MINIO_ENDPOINT}"
    }
  }
}
```

`${secret:KEY}` is resolved via `SecretProvider`:
- `HUGR_SECRET_PROVIDER=env` (default): reads from environment variables
- `HUGR_SECRET_PROVIDER=k8s`: reads from `/run/secrets/{KEY}`
- `HUGR_SECRET_PROVIDER=vault`: reads from HashiCorp Vault (future)

Plain text values pass through without resolution.

### idle_policy

Global idle culler settings. Per-profile `idle_timeout` and `max_age` override these.

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `cull_admins` | bool | `false` | Include admin workspaces in idle culling |
| `cull_interval` | int | `300` | Seconds between cull checks |

### Storage Auth Modes

| Mode | Description |
|------|-------------|
| `shared` | Hub-managed credentials from `storage.credentials`. User doesn't see them. |
| `user_token` | User's OIDC access token passed to storage. IAM decides access. Requires `required_scope`. |

If `auth: "user_token"` and `required_scope` is set but missing from user's token — volume silently skipped (warning in log).

## Environment Variables

Hub configuration via environment:

| Variable | Default | Description |
|----------|---------|-------------|
| `HUGR_PROFILES_PATH` | `/srv/jupyterhub/config/profiles.json` | Path to profiles.json |
| `HUGR_PROFILE_CLAIM` | — | OIDC claim for profile matching (e.g., `x-hub-profile`, `groups`) |
| `HUGR_ROLE_CLAIM` | `x-hugr-role` | OIDC claim for Hugr role |
| `HUGR_ADMIN_CLAIM` | — | OIDC claim for admin detection |
| `HUGR_ADMIN_VALUES` | `admin` | Comma-separated values that grant admin |
| `HUGR_ADMIN_USERS` | — | Static admin usernames (comma-separated) |
| `HUGR_SECRET_PROVIDER` | `env` | Secret provider: `env`, `k8s`, `vault` |
| `HUGR_SPAWNER` | `docker` | Orchestrator: `docker`, `kubernetes` |
| `HUGR_IDLE_TIMEOUT` | `3600` | Default idle timeout (seconds) |
| `HUGR_MAX_SERVER_AGE` | `86400` | Default max server age (seconds) |
| `HUGR_CULL_INTERVAL` | `300` | Idle culler check interval (seconds) |
| `HUGR_CULL_ADMINS` | `false` | Cull admin workspaces |

## Example: Minimal

```json
{
  "default_profile": "default",
  "profiles": {
    "default": {
      "display_name": "Default",
      "mem_limit": "4G",
      "cpu_limit": 2.0,
      "rank": 1
    }
  }
}
```

## Example: Multi-tier with S3

```json
{
  "default_profile": "small",
  "hub_limits": {"max_memory": "64G", "max_servers": 10},
  "profiles": {
    "small": {
      "display_name": "Small",
      "mem_limit": "4G",
      "cpu_limit": 2.0,
      "rank": 1,
      "volumes": {
        "shared": {"mount": "/home/jovyan/shared", "mode": "ro"}
      }
    },
    "large": {
      "display_name": "Large",
      "mem_limit": "32G",
      "cpu_limit": 8.0,
      "rank": 3,
      "volumes": {
        "shared": {"mount": "/home/jovyan/shared", "mode": "ro"},
        "s3-data": {"mount": "/home/jovyan/s3/data", "mode": "rw"}
      }
    }
  },
  "role_map": {"admin": "large", "viewer": "small"},
  "storage": {
    "volumes": {
      "shared": {"type": "local", "path": "/data/shared"},
      "s3-data": {"type": "s3", "credentials_ref": "aws", "bucket": "analytics-data"}
    },
    "credentials": {
      "aws": {
        "access_key_id": "${secret:AWS_ACCESS_KEY_ID}",
        "secret_access_key": "${secret:AWS_SECRET_ACCESS_KEY}"
      }
    }
  }
}
```

## Validation

Admin can validate profiles.json before saving:

```bash
bash /srv/jupyterhub/scripts/validate-profiles.sh /home/jovyan/hub-config/profiles.json
```
