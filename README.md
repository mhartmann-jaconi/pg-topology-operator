# pg-topology-operator

An Operator which makes sure, that the primary CNPG-Potgres instance is always running in the primary region in K8s. This happens in different scenarios:
1. A switchover -> a new region is designated as the new primary region: Operator will promote a sync-replica in the new primary region to the new primary instance
2. Initial Setup -> the first postgres instance is always the primary: Operator will add a [region](https://kubernetes.io/docs/reference/labels-annotations-taints/#topologykubernetesioregion)-based [`nodeSelector`](https://kubernetes.io/docs/concepts/scheduling-eviction/assign-pod-node/#nodeselector) to the primary postgres Pod, pointing to the primary region
3. Instance Failover -> the current primary instance gets unhealty: CNPG will decide to promote another postgres instance to primary; Operator intercepts that and makes sure that the new primary instance is running in the primary region

To promote a dedicated instance to new primary, the operator changes the field `status.targetPrimary` on the postgres cluster K8s resource (which is basically, what CNPG [also does](https://github.com/cloudnative-pg/cloudnative-pg/blob/main/internal/cmd/plugin/promote/promote.go)).

## Responsibilities

### Controllers

The **node controller** watches for K8s node events and stores information about all existing regions and which one is the primary region internally.

The **postgres controller** watches for PG cluster resources and stores information about number of standbies and current and desired primary instance.

The **pod controller** watches for K8s pods, belonging to PG clusters, and stores information about it's region and healthstate internally.

### Webhooks

The **pod webhook** intercepts K8s pod creation and applies a node selector to first instance, based on the region, which will point to the current primary region.

The **cluster webhook** intercepts PG cluster status changes and checks if it needs to change the selected target primary instance.

## Installation

```
helm install pg-topology helm -n cnpg-system
```

## Build and push

### docker

```
docker build . -f Dockerfile -t docker.lp.rsint.net/platform/pg-topology-operator:v0.11.1
docker push docker.lp.rsint.net/platform/pg-topology-operator:v0.11.1
```
or
```
export AD_USER=...
export AD_PASSWORD=...
make DOCKER_TAG=v0.11.1 docker-upload
```

### helm

```
export AD_USER=...
export AD_PASSWORD=...
make helm-upload
```

## cloudnative-pg behaviour
### Setup

1. The first replica created is the sole primary instance.
2. The next replicas are the sync-replicas (up to `postgresql.synchronous.number`).
3. All remaining replicas are replicated asynchronously.

The webhook must ensure, that the first replica is always deployed to the primary zone, and that all other instances are
distributed to all other existing zones equally.

### Switchover

1. Changes start to rollout one by one starting from the last index down to number 2.
2. Number 2 (must be sync-replicated!) gets promoted to new primary.
3. Number 1 gets changes applied.

As soon as the cluster becomes healthy again (all instances are up and running), the webhook must change the primary
instance to the sync-replica, which is running in primary zone.

### Failover

1. The primary instance is stopping or crashing.
2. The healthy sync-replica with lowest index becomes new primary instance.

The webhook must make sure that the new primary instance is at least in the same region as the primary zone and it must
watch for the cluster to become healthy again. As soon as this is the case, it must switch back the primary instance
again to the one in the primary zone.

## Implementation

### Logging

For logging, we use golangs [slog](https://go.dev/blog/slog) package. Exposed fields are:
* level - log level (DEBUG, INFO, WARN, ERROR or FATAL)
* msg - the actual log message
* cluster - (optional) name of the Postgres cluster
* err - (optional) underlying error message in error logs

### Visibility

The operator can expose prometheus metrics on port `9000`. To enable that, add the following to helm values
```
metrics:
  enabled: true
```

In addition [pprof debug endpoint](https://go.dev/wiki/Performance) can be enabled on port `1234` via helm setting
```
debug:
  enabled: true
```
After port forwarding
```
kubectl port-forward -n cnpg-system deployment/pg-topology-operator 1234
```
you can access it via http://localhost:1234/debug/pprof/

## TODO

* Build some kind of integration tests (for example, with [kind](https://kind.sigs.k8s.io/)). Or re-use the existing 
  implementation from local-datacenter-simulator
