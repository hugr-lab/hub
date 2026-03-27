"""Spawner-agnostic profile application — works with Docker and Kubernetes."""

import logging
import os
import re

log = logging.getLogger("hub_profiles.spawner")


def _parse_mem(value: str) -> int:
    """Parse memory string (e.g., '4G', '512M') to bytes. Returns 0 if empty/None."""
    if not value:
        return 0
    value = str(value).strip().upper()
    match = re.match(r'^(\d+(?:\.\d+)?)\s*([KMGT]?)B?$', value)
    if not match:
        return 0
    num = float(match.group(1))
    unit = match.group(2)
    multipliers = {'': 1, 'K': 1024, 'M': 1024**2, 'G': 1024**3, 'T': 1024**4}
    return int(num * multipliers.get(unit, 1))


def apply_profile(spawner, profile: dict, profile_name: str):
    """Apply resource profile to any spawner type.

    Sets memory limit, CPU limit, swap, GPU, custom image, and environment
    variables for jupyter-resource-usage display.
    """
    # Memory
    if profile.get("mem_limit"):
        spawner.mem_limit = profile["mem_limit"]
    if profile.get("mem_guarantee"):
        spawner.mem_guarantee = profile["mem_guarantee"]

    # CPU
    if profile.get("cpu_limit"):
        spawner.cpu_limit = profile["cpu_limit"]
    if profile.get("cpu_guarantee"):
        spawner.cpu_guarantee = profile["cpu_guarantee"]

    # Swap (Docker only)
    swap_factor = profile.get("swap_factor", 1.5)
    spawner_type = os.environ.get("HUGR_SPAWNER", "docker")
    if swap_factor and profile.get("mem_limit") and spawner_type == "docker":
        mem_bytes = _parse_mem(profile["mem_limit"])
        if mem_bytes > 0:
            if not hasattr(spawner, "extra_host_config"):
                spawner.extra_host_config = {}
            spawner.extra_host_config["memswap_limit"] = int(mem_bytes * swap_factor)

    # GPU
    if profile.get("gpu"):
        _apply_gpu(spawner, profile["gpu"], spawner_type)

    # Custom image
    if profile.get("image"):
        spawner.image = profile["image"]

    # Environment for jupyter-resource-usage
    spawner.environment["HUGR_RESOURCE_PROFILE"] = profile_name

    log.info(
        "Applied profile %r: mem=%s cpu=%s swap_factor=%s gpu=%s",
        profile_name,
        profile.get("mem_limit", "unlimited"),
        profile.get("cpu_limit", "unlimited"),
        swap_factor,
        profile.get("gpu"),
    )


def _apply_gpu(spawner, gpu_count: int, spawner_type: str):
    """Apply GPU config — dispatches to correct spawner API."""
    if spawner_type == "docker":
        try:
            import docker
            if not hasattr(spawner, "extra_host_config"):
                spawner.extra_host_config = {}
            spawner.extra_host_config.setdefault("device_requests", []).append(
                docker.types.DeviceRequest(
                    count=gpu_count,
                    driver="nvidia",
                    capabilities=[["gpu"]],
                )
            )
        except ImportError:
            log.warning("docker package not available — cannot configure GPU")
    elif spawner_type == "kubernetes":
        if hasattr(spawner, "extra_resource_limits"):
            spawner.extra_resource_limits = {"nvidia.com/gpu": str(gpu_count)}
    else:
        log.warning("GPU not supported for spawner type: %s", spawner_type)


def build_k8s_profiles(config: dict) -> list:
    """Generate KubeSpawner profile_list from profiles.json."""
    profiles = config.get("profiles", {})
    result = []
    for name, p in sorted(profiles.items(), key=lambda x: x[1].get("rank", 0)):
        override = {}
        if p.get("cpu_limit"):
            override["cpu_limit"] = p["cpu_limit"]
        if p.get("cpu_guarantee"):
            override["cpu_guarantee"] = p["cpu_guarantee"]
        if p.get("mem_limit"):
            override["mem_limit"] = p["mem_limit"]
        if p.get("mem_guarantee"):
            override["mem_guarantee"] = p["mem_guarantee"]
        if p.get("gpu"):
            override["extra_resource_limits"] = {"nvidia.com/gpu": str(p["gpu"])}
        if p.get("image"):
            override["image"] = p["image"]

        result.append({
            "display_name": p.get("display_name", name),
            "description": p.get("description", ""),
            "slug": name,
            "default": name == config.get("default_profile"),
            "kubespawner_override": override,
        })
    return result
