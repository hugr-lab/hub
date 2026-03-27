# Analytics Hub

Corporate analytics platform built on JupyterHub. Provides isolated user workspaces with Hugr-connected Jupyter kernels (GraphQL, DuckDB, Python), interactive data visualizations, and resource management. Part of the [hugr-lab](https://github.com/hugr-lab) ecosystem.

## Features

- **JupyterHub** with OIDC authentication (Keycloak, Microsoft Entra ID)
- **Resource profiles** — configurable memory, CPU, swap, GPU limits per role
- **Hugr kernels** — GraphQL and DuckDB kernels with Perspective viewer
- **Shared storage** — NFS, S3/MinIO FUSE mounts per profile
- **Idle management** — auto-stop inactive workspaces with per-profile timeouts
- **Monitoring** — OTel Collector for container metrics and logs (opt-in)
- **Python client** — hugr-client with Perspective viewer in notebooks

## Quick Start

### Prerequisites

- Docker Desktop
- Hugr server running
- OIDC provider (Keycloak or Entra ID)

### 1. Configure

```bash
cp .env.example .env
# Edit .env with your HUGR_URL, OIDC settings
```

### 2. Build singleuser image

```bash
docker compose -f docker-compose.local.yml build jupyter
```

### 3. Start Hub

```bash
# With OIDC (Keycloak/Entra):
docker compose -f docker-compose.dev.yml up -d --build

# Standalone JupyterLab (no OIDC):
docker compose -f docker-compose.local.yml up -d
```

### 4. Open

- Hub: http://localhost:8000
- Standalone: http://localhost:8888

## Architecture

```
OIDC Provider (Keycloak / Entra ID)
        │
        ▼
   JupyterHub ──── profiles.json ──── Resource Profiles
        │                                (mem, cpu, storage)
        ▼
   Workspace Containers
   ├── hugr-kernel (GraphQL)
   ├── duckdb-kernel (SQL)
   ├── Python kernel + hugr-client
   ├── Perspective viewer
   └── S3/NFS storage mounts
```

## Configuration

### Resource Profiles

`config/profiles.json` — defines workspace tiers, storage mounts, idle policies. Hot-reloadable without Hub restart.

See [config/README.md](config/README.md) for full schema documentation.

### Environment Variables

| Variable | Required | Description |
|----------|----------|-------------|
| `HUGR_URL` | yes | Hugr server base URL |
| `OIDC_CLIENT_SECRET` | yes | OIDC client secret |
| `HUB_BASE_URL` | yes | Hub external URL |
| `JUPYTERHUB_CRYPT_KEY` | yes | `openssl rand -hex 32` |
| `OIDC_CLIENT_ID` | no | Override OIDC client ID |
| `HUGR_PROFILE_CLAIM` | no | OIDC claim for profile assignment |
| `HUGR_ROLE_CLAIM` | no | OIDC claim for Hugr role (default: `x-hugr-role`) |
| `SINGLEUSER_IMAGE` | no | Custom workspace image |

See `.env.example` for all options.

### OIDC Providers

- **Keycloak**: Custom claims `x-hugr-role` + `x-hub-profile`
- **Entra ID**: App roles + groups claims. See [docs/entra-id-setup.md](docs/entra-id-setup.md)

## Docker Compose Files

| File | Purpose |
|------|---------|
| `docker-compose.dev.yml` | Hub + OIDC (Hugr/Keycloak on host) |
| `docker-compose.local.yml` | Standalone JupyterLab (no OIDC) |
| `docker-compose.yml` | Full stack (Hub + Hugr + Keycloak) |
| `docker-compose.monitoring.yml` | OTel Collector (opt-in) |

## Monitoring & Logging

Opt-in monitoring stack: OTel Collector (container metrics + logs) → Prometheus endpoint + Loki.

### Quick Start

```bash
# Start Hub + monitoring together:
docker compose -f docker-compose.dev.yml -f docker-compose.monitoring.yml up -d

# Or add monitoring to running Hub:
docker compose -f docker-compose.dev.yml -f docker-compose.monitoring.yml up -d otel-collector loki
```

### Endpoints

| Endpoint | Protocol | Content |
|----------|----------|---------|
| `:9464/metrics` | Prometheus | Container CPU, memory, network, disk I/O |
| `:8000/hub/metrics` | Prometheus | Active users, running servers, spawn times |
| `:3100` | Loki API | Container logs (all workspace + Hub containers) |

### Query Metrics

Connect Prometheus to scrape:

```yaml
# prometheus.yml
scrape_configs:
  - job_name: hub-containers
    static_configs:
      - targets: ['localhost:9464']
  - job_name: jupyterhub
    static_configs:
      - targets: ['localhost:8000']
    metrics_path: /hub/metrics
```

### Query Logs

Loki API at `:3100`. Query all logs:

```bash
# All logs from last 5 minutes
curl -s "http://localhost:3100/loki/api/v1/query_range?\
query=%7Bservice_name%3D~%22.%2B%22%7D&limit=10&\
start=$(date -u -v-5M +%s)000000000&end=$(date -u +%s)000000000"
```

Connect Grafana to Loki at `http://loki:3100` (within Docker network) or `http://localhost:3100` (from host).

### Architecture

```
Container logs (/var/lib/docker/containers/*/*.log)
    ↓ filelog receiver
OTel Collector
    ├─ docker_stats receiver → :9464/metrics (Prometheus)
    └─ filelog receiver → Loki (:3100)

JupyterHub → :8000/hub/metrics (Prometheus native)
```

### Without Monitoring

Hub operates normally without the monitoring stack. No performance impact, no errors.

## Singleuser Image

Includes:
- hugr-kernel, duckdb-kernel (Go binaries from releases)
- hugr-perspective-viewer, hugr-graphql-ide, hugr-duckdb-explorer (PyPI)
- hugr-client with Perspective viewer support
- jupyterlab-git, jupyterlab-execute-time, jupyterlab-favorites, jupyterlab-spreadsheet-editor
- jupyter-resource-usage (CPU/RAM/Disk monitoring)
- s3fs for S3/MinIO FUSE mounts
- FUSE entrypoint for cloud storage

## Related Repositories

| Repo | Purpose |
|------|---------|
| [hugr](https://github.com/hugr-lab/hugr) | Core DataMesh server |
| [hugr-kernel](https://github.com/hugr-lab/hugr-kernel) | Jupyter kernel for Hugr GraphQL |
| [duckdb-kernel](https://github.com/hugr-lab/duckdb-kernel) | DuckDB Jupyter kernel + Perspective viewer |
| [hugr-client](https://github.com/hugr-lab/hugr-client) | Python client for Hugr IPC |

## License

MIT
