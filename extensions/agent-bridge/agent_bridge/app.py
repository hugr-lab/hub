"""Agent Bridge — jupyter-server extension that manages the workspace hub-agent.

Responsibilities:
- Lazy-start hub-agent subprocess on first conversation request
- Health check and auto-restart if agent crashes
- Permission check via Hugr check_access before starting

Does NOT relay WebSocket — ChatWebSocketHandler in hub-chat handles routing.
"""

import asyncio
import json
import logging
import os
import subprocess
import time

import tornado.httpclient

logger = logging.getLogger("agent_bridge")

# Singleton agent process state
_agent_process: subprocess.Popen | None = None
_agent_lock = asyncio.Lock()

AGENT_LISTEN = os.environ.get("HUB_AGENT_LISTEN", "localhost:18888")
AGENT_HEALTH_URL = f"http://{AGENT_LISTEN}/health"


async def ensure_agent_running(access_token: str | None = None) -> bool:
    """Start workspace agent if not running. Returns True if agent is ready.

    Checks hub:agent_types permission before starting.
    Called by ChatWebSocketHandler when routing a local agent conversation.
    """
    global _agent_process

    async with _agent_lock:
        # Already running?
        if _agent_process is not None and _agent_process.poll() is None:
            # Verify health
            if await _check_health():
                return True
            # Process alive but not healthy — kill and restart
            logger.warning("agent process alive but unhealthy, restarting")
            _agent_process.terminate()
            _agent_process.wait(timeout=5)
            _agent_process = None

        # Check permission before starting
        if access_token:
            allowed = await _check_agent_permission(access_token)
            if not allowed:
                logger.warning("user not permitted to use personal-assistant agent type")
                return False

        # Start agent
        return await _start_agent()


async def _check_agent_permission(access_token: str) -> bool:
    """Check hub:agent_types.personal-assistant permission via Hugr."""
    hub_service_url = os.environ.get("HUB_SERVICE_URL", "")
    if not hub_service_url:
        return True  # no hub-service = dev mode, allow

    try:
        client = tornado.httpclient.AsyncHTTPClient()
        # Use /hugr proxy to call check_access with user's token
        body = json.dumps({"query": (
            '{ function { core { auth { check_access('
            'type_name: "hub:agent_types", fields: "personal-assistant"'
            ') { field enabled } } } } }'
        )})
        resp = await client.fetch(
            f"{hub_service_url}/hugr",
            method="POST",
            headers={
                "Content-Type": "application/json",
                "Authorization": f"Bearer {access_token}",
            },
            body=body,
            request_timeout=5,
        )
        data = json.loads(resp.body)
        entries = (
            data.get("data", {})
            .get("function", {})
            .get("core", {})
            .get("auth", {})
            .get("check_access") or []
        )
        for e in entries:
            if e.get("field") == "personal-assistant":
                return e.get("enabled", False)
        # No rule found = default-allow
        return True
    except Exception as e:
        logger.warning("permission check failed, allowing by default: %s", e)
        return True


async def _start_agent() -> bool:
    """Start hub-agent subprocess."""
    global _agent_process

    agent_bin = _find_agent_binary()
    if not agent_bin:
        logger.error("hub-agent binary not found")
        return False

    env = {**os.environ}
    # Ensure required env vars are set
    env.setdefault("HUB_AGENT_CONTEXT", "local")
    env.setdefault("HUB_AGENT_LISTEN", AGENT_LISTEN)
    env.setdefault("HUB_AGENT_HOME", os.path.expanduser("~/.agent"))

    # Skills directory — use system catalog if available
    skills_dir = "/usr/local/share/hub-agent/skills"
    if os.path.isdir(skills_dir):
        env.setdefault("AGENT_SKILLS_DIR", skills_dir)

    # Agent config — create with MCP servers if not exists
    config_path = os.path.expanduser("~/.agent/config.json")
    if not os.path.exists(config_path):
        os.makedirs(os.path.dirname(config_path), exist_ok=True)
        # Static MCP server config — will move to agent_types DB config (Spec H)
        mcp_servers = []
        if _binary_exists("result-store-mcp"):
            mcp_servers.append({
                "name": "result-store",
                "command": "result-store-mcp",
                "transport": "stdio",
            })
        if _binary_exists("kernel-mcp"):
            mcp_servers.append({
                "name": "kernel",
                "command": "kernel-mcp",
                "transport": "stdio",
            })
        if _binary_exists("sandbox-mcp"):
            mcp_servers.append({
                "name": "sandbox",
                "command": "sandbox-mcp",
                "transport": "stdio",
            })
        with open(config_path, "w") as f:
            json.dump({"max_turns": 15, "mcp_servers": mcp_servers}, f, indent=2)
    env.setdefault("AGENT_CONFIG", config_path)

    logger.info("starting hub-agent: %s", agent_bin)

    try:
        _agent_process = subprocess.Popen(
            [agent_bin],
            env=env,
            stdout=subprocess.PIPE,
            stderr=None,  # inherit — agent logs go to workspace stderr (visible in docker logs)
        )
    except Exception as e:
        logger.error("failed to start hub-agent: %s", e)
        return False

    # Wait for health check (up to 10s)
    for _ in range(20):
        await asyncio.sleep(0.5)
        if _agent_process.poll() is not None:
            # Process exited
            stderr = _agent_process.stderr.read().decode() if _agent_process.stderr else ""
            logger.error("hub-agent exited immediately: %s", stderr[:500])
            _agent_process = None
            return False
        if await _check_health():
            logger.info("hub-agent started and healthy (pid=%d)", _agent_process.pid)
            return True

    logger.error("hub-agent started but health check timed out")
    return False


async def _check_health() -> bool:
    """Check if agent is responding on health endpoint."""
    try:
        client = tornado.httpclient.AsyncHTTPClient()
        resp = await client.fetch(AGENT_HEALTH_URL, request_timeout=2)
        return resp.code == 200
    except Exception:
        return False


def _binary_exists(name: str) -> bool:
    """Check if a binary is available in PATH."""
    import shutil
    return shutil.which(name) is not None


def _find_agent_binary() -> str | None:
    """Find hub-agent binary in PATH or known locations."""
    import shutil
    path = shutil.which("hub-agent")
    if path:
        return path
    for candidate in ["/usr/local/bin/hub-agent", os.path.expanduser("~/bin/hub-agent")]:
        if os.path.isfile(candidate) and os.access(candidate, os.X_OK):
            return candidate
    return None


def _shutdown_agent():
    """Stop agent process on extension unload."""
    global _agent_process
    if _agent_process is not None and _agent_process.poll() is None:
        logger.info("stopping hub-agent (pid=%d)", _agent_process.pid)
        _agent_process.terminate()
        try:
            _agent_process.wait(timeout=5)
        except subprocess.TimeoutExpired:
            _agent_process.kill()
        _agent_process = None


def setup_handlers(web_app):
    """No HTTP handlers — agent-bridge only manages the agent process."""
    pass


def _load_jupyter_server_extension(server_app):
    setup_handlers(server_app.web_app)
    # Register shutdown hook
    import atexit
    atexit.register(_shutdown_agent)
    server_app.log.info("agent-bridge extension loaded (lazy agent start)")


def _jupyter_server_extension_points():
    return [{"module": "agent_bridge"}]
