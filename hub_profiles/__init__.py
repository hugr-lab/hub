"""Hub Profiles — resource limit management for Analytics Hub workspaces.

Provides hot-reloadable profiles from profiles.json, OIDC claim mapping,
budget enforcement, storage mounts, and secret management.
"""

from .loader import load_profiles
from .resolver import resolve_profile, extract_claim, check_budgets
from .spawner import apply_profile
from .secrets import get_secret_provider
from .storage import build_volumes

__all__ = [
    "load_profiles",
    "resolve_profile",
    "extract_claim",
    "check_budgets",
    "apply_profile",
    "get_secret_provider",
    "build_volumes",
]
