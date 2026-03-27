"""Custom idle culler that notifies users before stopping their servers.

Wraps jupyterhub-idle-culler logic:
1. Finds servers idle beyond (timeout - warn_before) seconds
2. Sends notification to user's server via JupyterHub API
3. Waits warn_before seconds
4. Stops the server

Runs as a JupyterHub managed service.
"""

import asyncio
import json
import logging
import os
import time

import httpx

log = logging.getLogger("idle-culler-notify")
logging.basicConfig(level=logging.INFO, format="%(asctime)s [%(name)s] %(message)s")

HUB_API_URL = os.environ.get("JUPYTERHUB_API_URL", "http://localhost:8081/hub/api")
HUB_TOKEN = os.environ.get("JUPYTERHUB_API_TOKEN", "")
IDLE_TIMEOUT = int(os.environ.get("HUGR_IDLE_TIMEOUT", "3600"))
MAX_AGE = int(os.environ.get("HUGR_MAX_SERVER_AGE", "86400"))
CULL_INTERVAL = int(os.environ.get("HUGR_CULL_INTERVAL", "300"))
CULL_ADMINS = os.environ.get("HUGR_CULL_ADMINS", "false").lower() == "true"
WARN_BEFORE = int(os.environ.get("HUGR_CULL_WARN_BEFORE", "300"))  # 5 minutes

# Track warned servers to avoid duplicate notifications
_warned: dict[str, float] = {}


async def get_users(client: httpx.AsyncClient) -> list:
    resp = await client.get(f"{HUB_API_URL}/users", params={"state": "active"})
    resp.raise_for_status()
    return resp.json()


async def notify_user(client: httpx.AsyncClient, username: str, minutes_left: int):
    """Send notification to user's running server via JupyterHub API."""
    try:
        # Post to the user's server notification endpoint
        # JupyterLab picks this up via its notification system
        resp = await client.post(
            f"{HUB_API_URL}/users/{username}/notify",
            json={
                "message": f"Your workspace will shut down in {minutes_left} minutes due to inactivity. Save your work.",
                "type": "warning",
            },
        )
        if resp.status_code == 404:
            # Notification endpoint may not exist — fall back to activity message
            log.debug("Notification endpoint not available for %s", username)
        else:
            log.info("Notified %s: shutdown in %d minutes", username, minutes_left)
    except Exception as e:
        log.warning("Failed to notify %s: %s", username, e)


async def stop_server(client: httpx.AsyncClient, username: str, server_name: str = ""):
    """Stop a user's server."""
    url = f"{HUB_API_URL}/users/{username}/server"
    if server_name:
        url = f"{HUB_API_URL}/users/{username}/servers/{server_name}"
    try:
        resp = await client.delete(url)
        if resp.status_code in (200, 202, 204):
            log.info("Stopped server for %s", username)
        else:
            log.warning("Failed to stop server for %s: %s", username, resp.status_code)
    except Exception as e:
        log.warning("Error stopping server for %s: %s", username, e)


async def check_and_cull():
    headers = {"Authorization": f"Bearer {HUB_TOKEN}"}
    async with httpx.AsyncClient(headers=headers, timeout=30) as client:
        users = await get_users(client)
        now = time.time()

        for user in users:
            username = user["name"]
            is_admin = user.get("admin", False)

            if is_admin and not CULL_ADMINS:
                continue

            for server_name, server in user.get("servers", {}).items():
                if not server.get("ready"):
                    continue

                last_activity = server.get("last_activity")
                if not last_activity:
                    continue

                # Parse ISO timestamp
                from datetime import datetime, timezone
                try:
                    activity_time = datetime.fromisoformat(last_activity.replace("Z", "+00:00"))
                    idle_seconds = now - activity_time.timestamp()
                except Exception:
                    continue

                # Check max age
                started = server.get("started")
                age_seconds = 0
                if started:
                    try:
                        start_time = datetime.fromisoformat(started.replace("Z", "+00:00"))
                        age_seconds = now - start_time.timestamp()
                    except Exception:
                        pass

                should_cull = False
                reason = ""

                if IDLE_TIMEOUT and idle_seconds > IDLE_TIMEOUT:
                    should_cull = True
                    reason = f"idle for {int(idle_seconds)}s (limit: {IDLE_TIMEOUT}s)"
                elif MAX_AGE and age_seconds > MAX_AGE:
                    should_cull = True
                    reason = f"age {int(age_seconds)}s (limit: {MAX_AGE}s)"

                if should_cull:
                    server_key = f"{username}/{server_name}"
                    warned_at = _warned.get(server_key)

                    if warned_at and (now - warned_at) >= WARN_BEFORE:
                        # Warning period elapsed — stop
                        log.info("Culling %s: %s", username, reason)
                        await stop_server(client, username, server_name)
                        _warned.pop(server_key, None)
                    elif not warned_at:
                        # First detection — warn
                        minutes = WARN_BEFORE // 60
                        log.info("Warning %s: will cull in %d minutes (%s)", username, minutes, reason)
                        await notify_user(client, username, minutes)
                        _warned[server_key] = now

                elif IDLE_TIMEOUT and idle_seconds > (IDLE_TIMEOUT - WARN_BEFORE):
                    # Approaching timeout — warn
                    server_key = f"{username}/{server_name}"
                    if server_key not in _warned:
                        remaining = int(IDLE_TIMEOUT - idle_seconds)
                        minutes = max(remaining // 60, 1)
                        log.info("Pre-warning %s: %d minutes until cull", username, minutes)
                        await notify_user(client, username, minutes)
                        _warned[server_key] = now
                else:
                    # Active — clear warning
                    server_key = f"{username}/{server_name}"
                    _warned.pop(server_key, None)


async def main():
    log.info(
        "Idle culler started: timeout=%ds, max_age=%ds, warn_before=%ds, interval=%ds, cull_admins=%s",
        IDLE_TIMEOUT, MAX_AGE, WARN_BEFORE, CULL_INTERVAL, CULL_ADMINS,
    )
    while True:
        try:
            await check_and_cull()
        except Exception as e:
            log.error("Culler error: %s", e)
        await asyncio.sleep(CULL_INTERVAL)


if __name__ == "__main__":
    asyncio.run(main())
