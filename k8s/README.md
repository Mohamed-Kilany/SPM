# Local Kubernetes and Argo CD

SPM ships the smallest practical real Kubernetes distribution for local and
single-node use: **k3s**. It runs as a privileged Docker container, uses
embedded SQLite, and does not require Docker Desktop's Kubernetes feature,
kind, minikube, or a separate k3d binary.

```
Docker Compose
└── k3s (one node, SQLite)
    └── argocd namespace
        ├── argocd-server (NodePort 30080)
        ├── argocd-application-controller
        ├── argocd-repo-server
        └── redis
```

The `k8s_bootstrap` one-shot container applies `argocd/kustomization.yaml`
with server-side apply after k3s is healthy. Server-side apply is required
because the Argo CD CRD annotations exceed the client-side apply limit. The API then reconciles Argo CD applications every 30
seconds; it does not accept deployment status from users.

Useful commands:

```powershell
docker compose exec k3s kubectl get nodes
docker compose exec k3s kubectl -n argocd get pods
docker compose exec k3s kubectl -n argocd get applications
docker compose logs -f k3s
```

If Docker Desktop does not allow privileged containers, enable the WSL 2
backend and make sure virtualization is available. k3s needs
`--privileged`, `--cgroupns=host`, and a writable cgroup filesystem.
