"""Secret management — resolve ${secret:KEY} references via configurable provider."""

import os
import logging
from pathlib import Path

log = logging.getLogger("hub_profiles.secrets")


class SecretNotFoundError(Exception):
    pass


class SecretProvider:
    """Base class for secret resolution."""

    def get(self, key: str) -> str:
        raise NotImplementedError


class EnvSecretProvider(SecretProvider):
    """Resolve secrets from environment variables."""

    def get(self, key: str) -> str:
        value = os.environ.get(key)
        if value is None:
            raise SecretNotFoundError(f"Environment variable '{key}' not set")
        return value


class K8sSecretProvider(SecretProvider):
    """Resolve secrets from Kubernetes secret files mounted at /run/secrets/."""

    def __init__(self, mount_path: str = "/run/secrets"):
        self._mount_path = mount_path

    def get(self, key: str) -> str:
        path = Path(self._mount_path) / key
        if not path.exists():
            raise SecretNotFoundError(f"Secret file not found: {path}")
        return path.read_text().strip()


# Singleton provider instance
_provider: SecretProvider | None = None


def get_secret_provider() -> SecretProvider:
    """Get or create the configured SecretProvider singleton."""
    global _provider
    if _provider is None:
        provider_type = os.environ.get("HUGR_SECRET_PROVIDER", "env")
        if provider_type == "k8s":
            _provider = K8sSecretProvider()
            log.info("Secret provider: Kubernetes secrets")
        else:
            _provider = EnvSecretProvider()
            log.info("Secret provider: environment variables")
    return _provider


def resolve_secret(value: str) -> str:
    """Resolve a value that may contain ${secret:KEY} reference.

    Returns:
        Resolved value if ${secret:KEY}, or original value if plain text.

    Raises:
        SecretNotFoundError if secret key not found.
    """
    if not isinstance(value, str):
        return value
    if not value.startswith("${secret:") or not value.endswith("}"):
        return value  # plain text passthrough

    key = value[9:-1]  # strip ${secret: and }
    return get_secret_provider().get(key)


def resolve_credentials(credentials: dict) -> dict:
    """Resolve all secret references in a credentials dict.

    Returns new dict with resolved values. Logs warnings for missing secrets.
    """
    resolved = {}
    for name, cred_set in credentials.items():
        resolved_set = {}
        for k, v in cred_set.items():
            try:
                resolved_set[k] = resolve_secret(v)
            except SecretNotFoundError as e:
                log.warning("Failed to resolve secret for %s.%s: %s", name, k, e)
        resolved[name] = resolved_set
    return resolved
