"""Storage volume builder — NFS, S3, Azure, GCS mount configuration."""

import json
import logging
import os

from .secrets import resolve_credentials

log = logging.getLogger("hub_profiles.storage")


def build_volumes(
    spawner,
    profile: dict,
    config: dict,
    auth_state: dict = None,
    access_token: str = None,
) -> dict:
    """Build volume mounts for spawner from profile + storage config.

    Docker: Returns volume dict {source: {bind: path, mode: ro/rw}} + MOUNT_* env vars.
    K8s: Appends to spawner.volume_mounts / spawner.volumes + MOUNT_* env vars for FUSE.
    """
    spawner_type = os.environ.get("HUGR_SPAWNER", "docker")
    storage = config.get("storage", {})
    all_volumes = storage.get("volumes", {})
    credentials = storage.get("credentials", {})
    profile_vols = profile.get("volumes", {})

    if not profile_vols:
        return {}

    # Resolve credentials once
    resolved_creds = resolve_credentials(credentials)

    volumes = {}

    for vol_name, mount_config in profile_vols.items():
        vol_def = all_volumes.get(vol_name)
        if not vol_def:
            log.warning("Volume '%s' referenced in profile but not defined in storage", vol_name)
            continue

        mount_path = mount_config.get("mount")
        if not mount_path:
            log.warning("Volume '%s' missing mount path in profile", vol_name)
            continue

        mode = mount_config.get("mode", "ro")
        vol_type = vol_def.get("type", "")
        auth_mode = vol_def.get("auth", "shared")

        # Check user_token scope
        if auth_mode == "user_token":
            required_scope = vol_def.get("required_scope")
            if required_scope and not _has_scope(access_token, required_scope):
                log.warning(
                    "Skipping volume '%s': required scope '%s' not in user token",
                    vol_name, required_scope,
                )
                continue

        if spawner_type == "kubernetes":
            if vol_type in ("nfs", "local"):
                # PVC-backed volume — assumes PVC pre-created by Helm chart or admin
                _add_k8s_pvc_volume(spawner, vol_name, mount_path, mode)
            elif vol_type in ("s3", "azure", "gcs"):
                # FUSE via entrypoint script (same as Docker) — pass MOUNT_* env vars
                _add_fuse_volume(
                    spawner, vol_name, vol_def, mount_path, mode,
                    auth_mode, resolved_creds, access_token,
                )
            else:
                log.warning("Unknown storage type '%s' for volume '%s'", vol_type, vol_name)
        else:
            if vol_type == "nfs":
                _add_nfs_volume(volumes, vol_name, vol_def, mount_path, mode)
            elif vol_type == "local":
                _add_local_volume(volumes, vol_def, mount_path, mode)
            elif vol_type in ("s3", "azure", "gcs"):
                _add_fuse_volume(
                    spawner, vol_name, vol_def, mount_path, mode,
                    auth_mode, resolved_creds, access_token,
                )
                _ensure_fuse_capabilities(spawner)
            else:
                log.warning("Unknown storage type '%s' for volume '%s'", vol_type, vol_name)

    return volumes


def _ensure_fuse_capabilities(spawner):
    """Add SYS_ADMIN + /dev/fuse to spawner (Docker only). Idempotent per spawner."""
    if not hasattr(spawner, "extra_host_config"):
        spawner.extra_host_config = {}
    cap_add = spawner.extra_host_config.get("cap_add", [])
    if "SYS_ADMIN" not in cap_add:
        cap_add.append("SYS_ADMIN")
        spawner.extra_host_config["cap_add"] = cap_add
    devices = spawner.extra_host_config.get("devices", [])
    if "/dev/fuse:/dev/fuse:rwm" not in devices:
        devices.append("/dev/fuse:/dev/fuse:rwm")
        spawner.extra_host_config["devices"] = devices
    spawner.extra_host_config["security_opt"] = ["apparmor:unconfined"]


def _add_k8s_pvc_volume(spawner, vol_name: str, mount_path: str, mode: str):
    """K8s: add PVC-backed volume mount to KubeSpawner.

    PVC claimName = vol_name (normalized: dots/underscores → hyphens, lowercase).
    The PVC must be pre-created (by Helm chart pvc-shared.yaml or admin).
    """
    safe_name = vol_name.replace(".", "-").replace("_", "-").lower()

    # KubeSpawner volume_mounts/volumes — ensure list, deduplicate by name/mountPath
    mounts = list(getattr(spawner, "volume_mounts", None) or [])
    mounts = [m for m in mounts if isinstance(m, dict)]
    # Skip if already mounted at this path
    if any(m.get("mountPath") == mount_path for m in mounts):
        log.info("K8s volume mount at %s already exists, skipping", mount_path)
        return
    mounts.append({
        "name": safe_name,
        "mountPath": mount_path,
        "readOnly": mode == "ro",
    })
    spawner.volume_mounts = mounts

    vols = list(getattr(spawner, "volumes", None) or [])
    vols = [v for v in vols if isinstance(v, dict)]
    # Skip if volume with same name already exists
    if not any(v.get("name") == safe_name for v in vols):
        vols.append({
            "name": safe_name,
            "persistentVolumeClaim": {
                "claimName": safe_name,
                "readOnly": mode == "ro",
            },
        })
    spawner.volumes = vols

    log.info("K8s PVC volume '%s' at %s (mode=%s)", safe_name, mount_path, mode)


def _add_nfs_volume(volumes: dict, vol_name: str, vol_def: dict, mount_path: str, mode: str):
    """NFS: use pre-created Docker volume (no FUSE, no SYS_ADMIN)."""
    volumes[vol_name] = {"bind": mount_path, "mode": mode}


def _add_local_volume(volumes: dict, vol_def: dict, mount_path: str, mode: str):
    """Local bind mount."""
    host_path = vol_def.get("path", "")
    if host_path:
        volumes[host_path] = {"bind": mount_path, "mode": mode}


def _add_fuse_volume(
    spawner, vol_name: str, vol_def: dict, mount_path: str, mode: str,
    auth_mode: str, resolved_creds: dict, access_token: str,
):
    """Cloud storage (S3/Azure/GCS): configure via MOUNT_* env vars for entrypoint script."""
    mount_config = {
        "type": vol_def["type"],
        "mount": mount_path,
        "read_only": mode == "ro",
    }

    # Copy storage-specific fields
    for key in ("bucket", "prefix", "region", "account", "container", "options"):
        if key in vol_def:
            mount_config[key] = vol_def[key]

    # Credentials
    if auth_mode == "shared":
        cred_ref = vol_def.get("credentials_ref", "")
        if cred_ref and cred_ref in resolved_creds:
            mount_config["credentials"] = resolved_creds[cred_ref]
    elif auth_mode == "user_token" and access_token:
        mount_config["access_token"] = access_token

    env_name = f"MOUNT_{vol_name.upper().replace('-', '_').replace('.', '_')}"
    value = json.dumps(mount_config)
    # KubeSpawner runs .format() on env values — escape braces
    spawner_type = os.environ.get("HUGR_SPAWNER", "docker")
    if spawner_type == "kubernetes":
        value = value.replace("{", "{{").replace("}", "}}")
    spawner.environment[env_name] = value


def _has_scope(access_token: str, required_scope: str) -> bool:
    """Check if access token contains required scope (best effort, no verification)."""
    if not access_token:
        return False
    try:
        import base64
        parts = access_token.split(".")
        if len(parts) < 2:
            return False
        payload = parts[1]
        padding = 4 - len(payload) % 4
        if padding != 4:
            payload += "=" * padding
        claims = json.loads(base64.urlsafe_b64decode(payload))
        scopes = claims.get("scp", "") or claims.get("scope", "")
        if isinstance(scopes, str):
            scopes = scopes.split()
        return required_scope in scopes
    except Exception:
        return False
