"""Profile loader — reads profiles.json on every spawn, validates, auto-creates default."""

import json
import logging
import os
from pathlib import Path

from .defaults import DEFAULT_CONFIG
from .schema import validate

log = logging.getLogger("hub_profiles.loader")

# Last known valid config for fallback
_last_valid: dict | None = None


def _profiles_path() -> Path:
    return Path(os.environ.get("HUGR_PROFILES_PATH", "/srv/jupyterhub/config/profiles.json"))


def load_profiles() -> dict:
    """Load profiles.json. Re-reads on every call (no caching).

    - If file missing: creates default config, returns it
    - If file invalid JSON: logs error, returns last valid
    - If file fails schema: logs error, returns last valid
    - If no last valid: returns hardcoded default
    """
    global _last_valid
    path = _profiles_path()

    # Auto-create if missing
    if not path.exists():
        log.info("profiles.json not found at %s — creating default", path)
        try:
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_text(json.dumps(DEFAULT_CONFIG, indent=2) + "\n")
        except OSError as e:
            log.error("Failed to create default profiles.json: %s", e)
        _last_valid = DEFAULT_CONFIG.copy()
        return _last_valid

    # Read file
    try:
        text = path.read_text()
    except OSError as e:
        log.error("Failed to read profiles.json: %s", e)
        return _last_valid or DEFAULT_CONFIG.copy()

    # Parse JSON
    try:
        config = json.loads(text)
    except json.JSONDecodeError as e:
        log.error("Invalid JSON in profiles.json: %s", e)
        return _last_valid or DEFAULT_CONFIG.copy()

    # Validate schema
    errors = validate(config)
    if errors:
        for err in errors:
            log.error("profiles.json validation: %s", err)
        return _last_valid or DEFAULT_CONFIG.copy()

    _last_valid = config
    return config
