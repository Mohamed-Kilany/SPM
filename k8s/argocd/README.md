# Argo CD on k3s

SPM reads deployments and health from Argo CD. The local stack runs a real
single-node **k3s** cluster and installs **Argo CD v3.5.6** into it.

## Why k3s

| Option | Runtime shape | Approximate node image | Notes |
| --- | --- | --- | --- |
| **k3s** | One lightweight Kubernetes binary/container | ~250 MB compressed | Embedded SQLite, no etcd, free Apache-2.0 |
| kind | Kubernetes nodes as Docker containers | ~1.3 GB | Excellent for conformance testing, heavier for this stack |
| k3d | k3s plus a CLI, load balancer and Docker plumbing | ~378 MB + helpers | Convenient multi-node wrapper, but needs an extra host binary |
| minikube | VM/container driver | ~1.9 GB | More moving parts than needed here |

SPM uses plain `rancher/k3s` so there is no extra host dependency. The k3s
container is the only Kubernetes node; Argo CD runs inside it.

## What is installed

- k3s v1.36.5 (Kubernetes v1.36.5) with Traefik, ServiceLB, metrics-server and
  local-path storage disabled.
- Argo CD v3.5.6, non-HA, with the API server exposed as NodePort `30080`.
- The Docker stack publishes that NodePort as `http://localhost:9090` and the
  SPM API reaches it internally at `http://k3s:30080`.

The local installation disables Argo CD authentication because it is bound to
the local Docker network. For a shared installation, remove
`server.disable.auth` from `kustomization.yaml`, install Argo CD with its normal
auth configuration, and set `ARGOCD_TOKEN` in `.env`.

## Bootstrap

`start-stack.ps1` and the `k8s_bootstrap` Compose service apply:

```sh
kubectl apply --server-side --force-conflicts -k /manifests/argocd
```

The manifests are vendored in `install.yaml`, so first start does not download
Argo CD manifests from GitHub. The Argo CD container images are pulled by k3s
on first start and then cached in the `k3s_data` volume.

## Registering an application

SPM matches an Argo CD Application to a project by either:

- `metadata.name` equal to the project `app_name`, or
- the label `spm.io/project-id` (or `spm.project/id`) set to the SPM project ID.

Optional labels:

```yaml
metadata:
  labels:
    spm.io/project-id: "<project-uuid>"
    spm.io/environment: "production"
```

The SPM API is read-only against Argo CD. It never creates, syncs, rolls back or
mutates applications.
