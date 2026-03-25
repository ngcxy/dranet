# DRANET Helm Chart

## Installation

From a local checkout:

```sh
helm upgrade --install dranet ./deployments/helm/dranet -n kube-system
```

## Configuration

The following table lists the configurable parameters and their default values:

| Parameter | Description | Default |
|-----------|-------------|---------|
| `nameOverride` | Override the chart name | `""` |
| `fullnameOverride` | Override the full release name | `""` |
| `image.repository` | Container image repository | `registry.k8s.io/networking/dranet` |
| `image.tag` | Container image tag | `stable` |
| `image.pullPolicy` | Image pull policy | `IfNotPresent` |
| `imagePullSecrets` | List of image pull secrets | `[]` |
| `rbac.create` | Create RBAC resources | `true` |
| `podAnnotations` | Annotations to add to pods | `{}` |
| `podLabels` | Labels to add to pods | `{}` |
| `logVerbosity` | Log verbosity level | `4` |
| `tolerations` | Pod tolerations | `[{operator: Exists, effect: NoSchedule}]` |
| `resources.requests.cpu` | CPU resource request | `100m` |
| `resources.requests.memory` | Memory resource request | `50Mi` |
| `resources.limits.cpu` | CPU resource limit | `""` (not set) |
| `resources.limits.memory` | Memory resource limit | `""` (not set) |
| `readinessProbe.httpGet.path` | Readiness probe HTTP path | `/healthz` |
| `readinessProbe.httpGet.port` | Readiness probe HTTP port | `9177` |

Parameters can be set at install time using `--set` or a custom values file:

```sh
helm upgrade --install dranet ./deployments/helm/dranet -n kube-system --set logVerbosity=6
helm upgrade --install dranet ./deployments/helm/dranet -n kube-system -f my-values.yaml
```
