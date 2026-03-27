"""Default profiles.json template — created when file is missing."""

DEFAULT_CONFIG = {
    "default_profile": "default",
    "hub_limits": {},
    "profiles": {
        "default": {
            "display_name": "Default (no limits)",
            "description": "Unlimited resources — configure profiles.json to set limits",
            "rank": 1,
        }
    },
    "role_map": {},
    "storage": {
        "volumes": {},
        "credentials": {},
    },
    "idle_policy": {},
}
