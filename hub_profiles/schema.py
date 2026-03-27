"""JSON schema validation for profiles.json."""

import json
import logging

log = logging.getLogger("hub_profiles.schema")

# Required top-level fields
REQUIRED_ROOT = {"default_profile", "profiles"}

# Required per-profile fields
REQUIRED_PROFILE = {"display_name", "rank"}

# Valid storage types
VALID_STORAGE_TYPES = {"nfs", "s3", "azure", "gcs", "local"}

# Valid storage auth modes
VALID_AUTH_MODES = {"shared", "user_token"}


class ValidationError(Exception):
    """Raised when profiles.json fails validation."""
    pass


def validate(config: dict) -> list[str]:
    """Validate profiles.json structure. Returns list of error messages (empty = valid)."""
    errors = []

    # Root structure
    if not isinstance(config, dict):
        return ["Root must be a JSON object"]

    for field in REQUIRED_ROOT:
        if field not in config:
            errors.append(f"Missing required field: {field}")

    # default_profile
    default = config.get("default_profile")
    profiles = config.get("profiles", {})

    if default and default not in profiles:
        errors.append(f"default_profile '{default}' not found in profiles")

    # Profiles
    if not isinstance(profiles, dict):
        errors.append("profiles must be a JSON object")
    else:
        for name, profile in profiles.items():
            if not isinstance(profile, dict):
                errors.append(f"Profile '{name}' must be a JSON object")
                continue
            for field in REQUIRED_PROFILE:
                if field not in profile:
                    errors.append(f"Profile '{name}' missing required field: {field}")
            if "rank" in profile and not isinstance(profile["rank"], (int, float)):
                errors.append(f"Profile '{name}' rank must be a number")
            if "swap_factor" in profile:
                sf = profile["swap_factor"]
                if not isinstance(sf, (int, float)) or sf < 0:
                    errors.append(f"Profile '{name}' swap_factor must be >= 0")
            if "volumes" in profile:
                if not isinstance(profile["volumes"], dict):
                    errors.append(f"Profile '{name}' volumes must be a JSON object")
                else:
                    for vol_name, mount in profile["volumes"].items():
                        if not isinstance(mount, dict):
                            errors.append(f"Profile '{name}' volume '{vol_name}' must be {{mount, mode}}")
                        elif "mount" not in mount:
                            errors.append(f"Profile '{name}' volume '{vol_name}' missing 'mount' path")

    # role_map
    role_map = config.get("role_map", {})
    if not isinstance(role_map, dict):
        errors.append("role_map must be a JSON object")
    else:
        for role, profile_name in role_map.items():
            if profile_name not in profiles:
                errors.append(f"role_map '{role}' references unknown profile '{profile_name}'")

    # hub_limits
    hub_limits = config.get("hub_limits", {})
    if not isinstance(hub_limits, dict):
        errors.append("hub_limits must be a JSON object")

    # storage
    storage = config.get("storage", {})
    if isinstance(storage, dict):
        volumes = storage.get("volumes", {})
        if isinstance(volumes, dict):
            for vol_name, vol_def in volumes.items():
                if not isinstance(vol_def, dict):
                    errors.append(f"Storage volume '{vol_name}' must be a JSON object")
                    continue
                if "type" not in vol_def:
                    errors.append(f"Storage volume '{vol_name}' missing 'type'")
                elif vol_def["type"] not in VALID_STORAGE_TYPES:
                    errors.append(f"Storage volume '{vol_name}' invalid type '{vol_def['type']}'")
                if "auth" in vol_def and vol_def["auth"] not in VALID_AUTH_MODES:
                    errors.append(f"Storage volume '{vol_name}' invalid auth '{vol_def['auth']}'")

    return errors
