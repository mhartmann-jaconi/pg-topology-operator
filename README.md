# ipoque-pg-webhook

This webhook is responsible for assigning a
[zone](https://kubernetes.io/docs/reference/labels-annotations-taints/#topologykubernetesiozone)-based
[`nodeSelector`](https://kubernetes.io/docs/concepts/scheduling-eviction/assign-pod-node/#nodeselector) to Pods and PVCs
created by [cloudnative-pg](https://cloudnative-pg.io/). In addition, this will also make sure that the primary Postgres
instance is always running in the primary zone.

## Installation

```
helm install pg-topology helm -n cnpg-system
```

## Build and push

### docker

```
docker build . -f Dockerfile -t docker.lp.rsint.net/platform/pg-topology-operator:v0.7.3
docker push docker.lp.rsint.net/platform/pg-topology-operator:v0.7.3
```

### helm

```
helm package helm
chart="$(ls -t1 pg*.tgz | head -n1)"
curl -k -u yourAD:yourPW -XPUT "https://helm.lp.rsint.net/helm-dev/rs/coc/platform/$chart" -T $chart
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

## TODO

* Build some kind of integration tests (for example, with [kind](https://kind.sigs.k8s.io/)).
