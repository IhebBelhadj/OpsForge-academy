---
title: Labels, selectors and topology
minutes: 8
resources:
  - { title: "Kubernetes docs: Labels and selectors", url: "https://kubernetes.io/docs/concepts/overview/working-with-objects/labels/" }
  - { title: "Kubernetes docs: Well-known labels", url: "https://kubernetes.io/docs/reference/labels-annotations-taints/" }
---
Kubernetes objects find each other by **labels**, not by name. A label is a key/value pair in an object's
`metadata.labels`:

```yaml
metadata:
  name: web-7d4b9c8f6-x2kqp
  labels:
    app: web
    tier: frontend
```

A **selector** is a query over labels. A Deployment finds its pods with one, a Service finds the pods to send
traffic to with one, and so do you:

```bash
opsforge kubectl get pods -l app=web                  # equality
opsforge kubectl get pods -l 'tier in (frontend,api)' # set-based
opsforge kubectl get pods -l '!canary'                # without the label
```

Labels are cheap and you can add as many as you like: what app something belongs to, its tier, its version, its
team. **Annotations** look similar but are for information nobody selects on (a git commit, a contact, tool
settings).

## Nodes have labels too

The scheduler places pods using node labels. Some are set for you, and are the same in every cluster:

| Label | Meaning |
|---|---|
| `kubernetes.io/hostname` | the node's name |
| `topology.kubernetes.io/region` | the region the node is in |
| `topology.kubernetes.io/zone` | the zone (a data centre) within the region |

Cloud providers set the topology labels when a node joins. This lab does the same: each node is labelled with its
site, so ${node:c} is in region `c`. Show them as columns with `-L`:

```bash
opsforge kubectl get nodes -L topology.kubernetes.io/region,topology.kubernetes.io/zone
```

Because "region" is just a label, anything that uses it (spreading pods over regions, keeping a cache in one
region) works the same here as in a real cloud. In the next lab you read the topology labels, add a label of your
own, and find the rule that keeps the demo application one pod per region.
