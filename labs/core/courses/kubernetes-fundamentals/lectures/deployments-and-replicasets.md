---
title: Deployments, ReplicaSets and spreading
minutes: 10
resources:
  - { title: "Kubernetes docs: Deployments", url: "https://kubernetes.io/docs/concepts/workloads/controllers/deployment/" }
  - { title: "Kubernetes docs: Pod topology spread constraints", url: "https://kubernetes.io/docs/concepts/scheduling-eviction/topology-spread-constraints/" }
---
You could create pods one by one, but a bare pod is never replaced: when its node dies, it is gone. Workloads are
run by controllers instead.

## Three layers

- A **ReplicaSet** keeps a number of identical pods running, made from its pod template. Fewer than `replicas`: it
  creates pods; more: it deletes some.
- A **Deployment** manages ReplicaSets. When you change its pod template (a new image, say), it creates a new
  ReplicaSet and moves pods over to it step by step: a **rollout**. The old ReplicaSet stays, scaled to zero, so
  you can roll back.
- The **pods** carry a `pod-template-hash` label, computed from the template, which tells the ReplicaSets' pods
  apart.

```bash
opsforge kubectl -n demo get deploy,rs,pods
```

The lab's demo application is a Deployment called `whoami`, one pod per region. You almost always create
Deployments and let them handle the rest; scaling one is a change to a single number:

```bash
opsforge kubectl -n lab scale deploy web --replicas=5
```

## Where the pods go

By default the scheduler only *prefers* to spread a Deployment's pods over nodes and zones. When nodes are busy or
full it will put every replica in one region, and one regional outage then takes them all.

A **topology spread constraint** turns the preference into a rule:

```yaml
topologySpreadConstraints:
  - maxSkew: 1
    topologyKey: topology.kubernetes.io/region
    whenUnsatisfiable: DoNotSchedule
    labelSelector:
      matchLabels:
        app: web
    matchLabelKeys: [pod-template-hash]
```

- `topologyKey`: the node label that defines the domains, here regions.
- `maxSkew: 1`: the fullest and the emptiest region may differ by at most one pod. Five pods over three regions
  can only be 2/2/1.
- `DoNotSchedule`: a pod that would break the rule stays Pending rather than being placed anyway.
- `matchLabelKeys: [pod-template-hash]`: count only pods of the same revision. Without it, during a rollout the
  old pods still being replaced are counted too, and the new ones pile up in the regions the old ones are leaving.

> [!WARNING]
> The scheduler applies the constraint only when it **places** a pod. Pods already running are never moved, so a
> skew created by a failure stays until the pods are replaced, for example by a rollout.
