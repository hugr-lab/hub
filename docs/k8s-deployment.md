# Kubernetes Deployment Guide

Analytics Hub can be deployed on Kubernetes using the Helm chart in `k8s/hugr-hub/`.
The chart wraps [Zero to JupyterHub (z2jh)](https://z2jh.jupyter.org/) as a subchart
and adds Hub-specific configuration: OIDC auto-discovery, resource profiles, storage mounts,
and idle culler.

## Prerequisites

- Kubernetes cluster (minikube, EKS, AKS, GKE)
- Helm 3.x or 4.x
- Hugr server accessible from the cluster
- OIDC provider (Keycloak, Entra ID, etc.) accessible from the cluster
- Hub images published or loaded into the cluster

## Architecture

```
                    ┌──────────────────────────────────┐
                    │           K8s Cluster             │
  User ──► Ingress/│  ┌───────┐     ┌──────────────┐  │
           NodePort│  │ Proxy │────►│   Hub Pod     │  │
                    │  └───────┘     │ (KubeSpawner) │  │
                    │                └──────┬───────┘  │
                    │                       │ spawns   │
                    │        ┌──────────────┴───────┐  │
                    │        │   Workspace Pod       │  │
                    │        │  (JupyterLab + Hugr)  │  │
                    │        │                       │  │
                    │        │  /home/jovyan  ← PVC  │  │
                    │        │  /shared      ← PVC   │  │
                    │        │  /s3/data     ← CSI   │  │
                    │        └───────────────────────┘  │
                    └──────────────────────────────────┘
                                     │
                              ┌──────┴──────┐
                              │ Hugr Server │
                              │ OIDC Provider│
                              └─────────────┘
```

## Quick Start (Minikube)

### 1. Start Minikube

```bash
minikube start --memory=16384 --cpus=4
```

### 2. Build Images Inside Minikube

```bash
eval $(minikube docker-env)
docker build -f Dockerfile.hub -t hub-jupyterhub:latest .
docker build -f Dockerfile.singleuser -t hub-jupyter:latest .
```

### 3. Install Chart Dependencies

```bash
cd k8s/hugr-hub
helm dependency build .
```

### 4. Deploy

```bash
helm install hugr-hub . \
  -f values-minikube.yaml \
  --set hub.oidc.clientSecret=YOUR_KEYCLOAK_SECRET \
  -n hugr-hub --create-namespace
```

### 5. Access Hub

```bash
kubectl port-forward -n hugr-hub svc/proxy-public 8000:80
```

Open http://localhost:8000

### 6. Mount Host Directories (Optional)

To share files from host into minikube (e.g., `shared/` directory):

```bash
# Run in a separate terminal — must stay alive
minikube mount ./shared:/data/shared
```

Then create a static PV/PVC pointing to `/data/shared` on the minikube node.

## Configuration

All Hub settings are configured through `hub.*` values in `values.yaml`.
These are automatically transformed into environment variables via
`configmap-hub-env.yaml` template — no need to duplicate them under
`jupyterhub.hub.extraEnv`.

### Required Settings

| Value | Description |
|-------|-------------|
| `hub.hugrUrl` | Hugr server URL (e.g., `http://hugr:15000`) |
| `hub.oidc.clientSecret` | OIDC client secret |
| `hub.baseDomain` | Hub external URL for OIDC redirect |

### Optional Settings

| Value | Default | Description |
|-------|---------|-------------|
| `hub.oidc.clientId` | auto-discovered | OIDC client ID |
| `hub.oidc.tlsSkipVerify` | `false` | Skip TLS for OIDC provider |
| `hub.hugrTlsSkipVerify` | `false` | Skip TLS for Hugr server |
| `hub.admin.users` | `""` | Comma-separated admin usernames |
| `hub.admin.claim` | `""` | OIDC claim for admin detection |
| `hub.admin.values` | `"admin"` | Claim values granting admin |
| `hub.profiles.claim` | `""` | OIDC claim for profile assignment |
| `hub.profiles.roleClaim` | `"x-hugr-role"` | OIDC claim for Hugr role |
| `hub.secretProvider` | `"k8s"` | Secret provider: `env`, `k8s` |
| `hub.connectionName` | `"default"` | Managed connection name |

### Images

| Value | Default | Description |
|-------|---------|-------------|
| `singleuser.image.name` | `hugr-lab/hub-singleuser` | Workspace image |
| `singleuser.image.tag` | `latest` | Image tag |
| `jupyterhub.hub.image.name` | `hugr-lab/hub` | Hub image |
| `jupyterhub.hub.image.tag` | `latest` | Hub image tag |

### User Storage

| Value | Default | Description |
|-------|---------|-------------|
| `singleuser.storage.capacity` | `10Gi` | User home PVC size |
| `singleuser.storage.storageClassName` | cluster default | StorageClass |

## Profiles

profiles.json is stored on a PVC (`hugr-hub-config`), not a ConfigMap.
This allows admins to edit profiles without `helm upgrade`:

```bash
# Get hub pod name
HUB_POD=$(kubectl get pod -n hugr-hub -l component=hub -o jsonpath='{.items[0].metadata.name}')

# Download current profiles
kubectl cp hugr-hub/$HUB_POD:/opt/hub/config/profiles.json ./profiles.json

# Edit profiles.json locally, then upload
kubectl cp ./profiles.json hugr-hub/$HUB_POD:/opt/hub/config/profiles.json
```

Changes take effect on the next workspace spawn (hot-reload, no restart needed).

Initial profiles.json is seeded from `profiles` value on first install.
If empty, a default unlimited profile is auto-created.

### Admin Access to profiles.json

Mount `hugr-hub-config` PVC into admin workspace by adding it to
profiles.json storage volumes:

```json
{
  "profiles": {
    "admin-profile": {
      "volumes": {
        "hugr-hub-config": {
          "mount": "/home/jovyan/hub-config",
          "mode": "rw"
        }
      }
    }
  },
  "storage": {
    "volumes": {
      "hugr-hub-config": { "type": "local" }
    }
  }
}
```

Admin can then edit `/home/jovyan/hub-config/profiles.json` directly from JupyterLab.

## Storage

### Shared Volumes (PVC)

Define shared PVCs in values:

```yaml
sharedVolumes:
  shared-data:
    capacity: "50Gi"
    storageClassName: "nfs"
    accessMode: "ReadWriteMany"
```

The Helm chart creates PVCs automatically. Reference them in profiles.json:

```json
{
  "storage": {
    "volumes": {
      "shared-data": { "type": "local" }
    }
  }
}
```

### S3 Storage (CSI)

For S3-compatible storage (AWS S3, MinIO), use the
[csi-s3](https://github.com/yandex-cloud/k8s-csi-s3) CSI driver:

```bash
# Install CSI driver
helm repo add yandex-s3 https://yandex-cloud.github.io/k8s-csi-s3/charts
helm install csi-s3 yandex-s3/csi-s3 \
  -n csi-s3 --create-namespace \
  --set secret.endpoint=http://minio:9000 \
  --set secret.accessKey=ACCESS_KEY \
  --set secret.secretKey=SECRET_KEY

# Disable fsGroup on CSI driver (FUSE doesn't support chown)
kubectl patch csidriver ru.yandex.s3.csi --type=merge \
  -p '{"spec":{"fsGroupPolicy":"None"}}'
```

Create a static PV/PVC for an existing bucket:

```yaml
apiVersion: v1
kind: PersistentVolume
metadata:
  name: s3-data-pv
spec:
  capacity:
    storage: 5Gi
  accessModes: [ReadWriteMany]
  persistentVolumeReclaimPolicy: Retain
  csi:
    driver: ru.yandex.s3.csi
    volumeHandle: my-bucket-name    # S3 bucket name
    controllerPublishSecretRef:
      name: csi-s3-secret
      namespace: csi-s3
    nodePublishSecretRef:
      name: csi-s3-secret
      namespace: csi-s3
    nodeStageSecretRef:
      name: csi-s3-secret
      namespace: csi-s3
    volumeAttributes:
      capacity: "5Gi"
      mounter: geesefs
      options: "--memory-limit 64 --dir-mode 0777 --file-mode 0666"
---
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: s3-data
  namespace: hugr-hub
spec:
  accessModes: [ReadWriteMany]
  storageClassName: ""
  volumeName: s3-data-pv
  resources:
    requests:
      storage: 5Gi
```

Reference in profiles.json as `type: "local"` (it's a PVC mount):

```json
{
  "profiles": {
    "analyst": {
      "volumes": {
        "s3-data": { "mount": "/home/jovyan/s3/data", "mode": "rw" }
      }
    }
  },
  "storage": {
    "volumes": {
      "s3-data": { "type": "local" }
    }
  }
}
```

**Important**: Disable `fsGid` in singleuser config when using S3 CSI:

```yaml
jupyterhub:
  singleuser:
    fsGid: null
    extraPodConfig:
      securityContext:
        fsGroupChangePolicy: OnRootMismatch
```

### Azure Blob Storage (CSI)

For Azure, use the [Azure Blob CSI driver](https://github.com/kubernetes-sigs/blob-csi-driver):

```bash
helm repo add blob-csi-driver https://raw.githubusercontent.com/kubernetes-sigs/blob-csi-driver/master/charts
helm install blob-csi blob-csi-driver/blob-csi-driver -n kube-system
```

Create a Secret with storage account credentials and a static PV/PVC
pointing to your blob container. Same profiles.json pattern as S3.

> **Note**: Azure Blob CSI requires blobfuse2 on the node. On ARM64 minikube
> this is not available. Use a real AKS cluster for Azure Blob testing.

## Production Deployment

See `k8s/hugr-hub/values-production.yaml` for a complete example.

Key differences from minikube:

| Setting | Minikube | Production |
|---------|----------|------------|
| Images | Local (`pullPolicy: Never`) | Registry (`pullPolicy: Always`) |
| Proxy | `NodePort` | `ClusterIP` + Ingress |
| Storage | `hostPath` default | Cloud StorageClass (gp3, managed-premium) |
| TLS | None | cert-manager + Ingress |
| Secrets | `secretProvider: env` | `secretProvider: k8s` (file-mounted K8s Secrets) |
| User scheduler | Disabled | Enabled |
| Hub resources | Default | CPU/memory limits set |

### Ingress + TLS

```yaml
ingress:
  enabled: true
  className: "nginx"
  annotations:
    cert-manager.io/cluster-issuer: "letsencrypt-prod"
  hosts:
    - "hub.example.com"
  tls:
    - secretName: "hub-tls"
      hosts:
        - "hub.example.com"
```

## Troubleshooting

```bash
# Hub pod logs
kubectl logs -n hugr-hub -l component=hub

# Workspace pod logs
kubectl logs -n hugr-hub jupyter-USERNAME -c notebook

# Pod events (scheduling, volume mount issues)
kubectl describe pod -n hugr-hub jupyter-USERNAME

# PVC status
kubectl get pvc -n hugr-hub

# Check profiles.json on PVC
kubectl exec -n hugr-hub deploy/hub -- cat /opt/hub/config/profiles.json

# CSI driver logs (S3)
kubectl logs -n csi-s3 -l app=csi-s3 -c csi-s3

# Force delete stuck pod
kubectl delete pod jupyter-USERNAME -n hugr-hub --force --grace-period=0
```

### Common Issues

**Workspace pod stuck in `Init:0/1`**: Usually a volume mount issue. Check
`kubectl describe pod` for mount errors. CSI volumes may take time on first mount.

**S3 mount hangs (fsGroup)**: Ensure `fsGid: null` is set and CSI driver has
`fsGroupPolicy: None`. K8s tries to chown all files on FUSE mounts which hangs.

**`volumeHandle` mismatch**: For S3 CSI, `volumeHandle` in the PV must match
the bucket name exactly. CSI driver uses it as the bucket name to mount.

**Hub starts with DummyAuthenticator**: The `zz-load-hub-config` extraConfig
must be present to load our `jupyterhub_config.py`. Verify the Hub image
contains `/opt/hub/jupyterhub_config.py`.

**Image not found**: On minikube, build images inside minikube docker
(`eval $(minikube docker-env)`) and set `pullPolicy: Never`.
