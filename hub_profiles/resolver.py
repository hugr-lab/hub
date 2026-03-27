"""Profile resolution — extract OIDC claims, match to profiles, check budgets."""

import json
import base64
import logging
import os

from .spawner import _parse_mem

log = logging.getLogger("hub_profiles.resolver")


class ResourceBudgetError(Exception):
    """Raised when spawn would exceed user or hub resource budget."""
    pass


def extract_claim(auth_state: dict, claim_name: str) -> list[str]:
    """Extract claim value from auth state, always returns list.

    Handles string (wrapped in list) and array values.
    Tries auth_state directly, then decoded id_token.
    """
    if not auth_state or not claim_name:
        return []

    # Direct from auth_state
    value = auth_state.get(claim_name)

    # Try from decoded id_token
    if value is None:
        value = _extract_from_id_token(auth_state, claim_name)

    if value is None:
        return []
    if isinstance(value, str):
        return [value]
    if isinstance(value, list):
        return [str(v) for v in value]
    return []


def _extract_from_id_token(auth_state: dict, claim_name: str):
    """Extract claim from id_token payload (JWT without verification)."""
    token_response = auth_state.get("token_response", {})
    if not isinstance(token_response, dict):
        return None
    id_token = token_response.get("id_token", "")
    if not id_token:
        return None
    try:
        parts = id_token.split(".")
        if len(parts) < 2:
            return None
        payload = parts[1]
        padding = 4 - len(payload) % 4
        if padding != 4:
            payload += "=" * padding
        claims = json.loads(base64.urlsafe_b64decode(payload))
        return claims.get(claim_name)
    except Exception:
        return None


def resolve_profile(
    spawner,
    auth_state: dict,
    config: dict,
) -> list[tuple[str, dict]]:
    """Resolve available profiles for user.

    Returns list of (profile_name, profile_dict) tuples.
    If multiple — user gets selection UI. If single — auto-apply.
    Empty list — spawn should use default.

    Priority:
    1. Profile claim match (by value field or name)
    2. Role map fallback
    3. Default profile
    """
    profiles = config.get("profiles", {})
    if not profiles:
        return []

    # 1. Profile claim match
    claim_name = os.environ.get("HUGR_PROFILE_CLAIM", "")
    if claim_name:
        claim_values = extract_claim(auth_state, claim_name)
        if claim_values:
            matched = _match_profiles(claim_values, profiles)
            if matched:
                matched.sort(key=lambda x: x[1].get("rank", 0), reverse=True)
                return matched

    # 2. Role map fallback
    role_claim = os.environ.get("HUGR_ROLE_CLAIM", "x-hugr-role")
    roles = extract_claim(auth_state, role_claim)
    role_map = config.get("role_map", {})
    for role in roles:
        if role in role_map and role_map[role] in profiles:
            pname = role_map[role]
            return [(pname, profiles[pname])]

    # 3. Default
    default = config.get("default_profile", "default")
    if default in profiles:
        return [(default, profiles[default])]

    return []


def _match_profiles(claim_values: list[str], profiles: dict) -> list[tuple[str, dict]]:
    """Match claim values against profiles (by value field or name)."""
    matched = []
    for pname, profile in profiles.items():
        match_value = profile.get("value") or pname
        if match_value in claim_values:
            matched.append((pname, profile))
    return matched


def check_budgets(spawner, profile: dict, config: dict):
    """Check user-level and hub-level budgets before spawn.

    Raises ResourceBudgetError if any budget would be exceeded.
    """
    hub_limits = config.get("hub_limits", {})

    # Count running servers for this user (exclude current spawner being started)
    user_servers = []
    if hasattr(spawner, "user") and hasattr(spawner.user, "spawners"):
        user_servers = [s for s in spawner.user.spawners.values() if s.active and s is not spawner]

    new_mem = _parse_mem(profile.get("mem_limit", "0"))
    new_cpu = profile.get("cpu_limit", 0) or 0

    # User budget
    max_servers = profile.get("user_max_servers", 0)
    max_mem = _parse_mem(profile.get("user_max_memory", "0"))
    max_cpu = profile.get("user_max_cpu", 0) or 0

    if max_servers and len(user_servers) >= max_servers:
        raise ResourceBudgetError(
            f"Maximum servers reached ({max_servers}). "
            "Stop an existing server before starting a new one."
        )

    if max_mem:
        user_mem = sum(_parse_mem(getattr(s, "mem_limit", "0") or "0") for s in user_servers)
        if (user_mem + new_mem) > max_mem:
            raise ResourceBudgetError(
                f"Memory budget exceeded. "
                f"Used: {_format_mem(user_mem)}, "
                f"Requested: {_format_mem(new_mem)}, "
                f"Budget: {_format_mem(max_mem)}."
            )

    if max_cpu:
        user_cpu = sum(getattr(s, "cpu_limit", 0) or 0 for s in user_servers)
        if (user_cpu + new_cpu) > max_cpu:
            raise ResourceBudgetError(
                f"CPU budget exceeded. "
                f"Used: {user_cpu}, Requested: {new_cpu}, Budget: {max_cpu}."
            )

    # Hub budget
    hub_max_servers = hub_limits.get("max_servers", 0)
    hub_max_mem = _parse_mem(hub_limits.get("max_memory", "0"))
    hub_max_cpu = hub_limits.get("max_cpu", 0) or 0

    if hub_max_servers or hub_max_mem or hub_max_cpu:
        all_servers = _get_all_active_servers(spawner)

        if hub_max_servers and len(all_servers) >= hub_max_servers:
            raise ResourceBudgetError(
                f"Hub server limit reached ({hub_max_servers}). "
                "Please try again later."
            )

        if hub_max_mem:
            total_mem = sum(_parse_mem(getattr(s, "mem_limit", "0") or "0") for s in all_servers)
            if (total_mem + new_mem) > hub_max_mem:
                raise ResourceBudgetError(
                    "Hub memory limit reached. Please try again later."
                )

        if hub_max_cpu:
            total_cpu = sum(getattr(s, "cpu_limit", 0) or 0 for s in all_servers)
            if (total_cpu + new_cpu) > hub_max_cpu:
                raise ResourceBudgetError(
                    "Hub CPU limit reached. Please try again later."
                )


def _get_all_active_servers(spawner) -> list:
    """Get all active servers across all users."""
    try:
        app = spawner.app
        servers = []
        for user in app.users.values():
            for s in user.spawners.values():
                if s.active:
                    servers.append(s)
        return servers
    except Exception:
        return []


def _format_mem(bytes_val: int) -> str:
    """Format bytes as human-readable string."""
    if bytes_val >= 1024**3:
        return f"{bytes_val / 1024**3:.0f}G"
    if bytes_val >= 1024**2:
        return f"{bytes_val / 1024**2:.0f}M"
    return f"{bytes_val}B"
