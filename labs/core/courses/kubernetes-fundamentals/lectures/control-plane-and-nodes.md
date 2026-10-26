---
title: The control plane and the nodes
minutes: 10
resources:
  - { title: "Kubernetes docs: Cluster components", url: "https://kubernetes.io/docs/concepts/overview/components/" }
  - { title: "K3s docs: Architecture", url: "https://docs.k3s.io/architecture" }
---
A container runtime starts containers on one machine. It does not decide *which* machine, restart a container
when its machine dies, or keep three copies running. Kubernetes does: it turns a group of machines into one place
to run containers, and keeps what runs there matching what you asked for.

A cluster has two halves: the **control plane**, which decides, and the **nodes**, which run the containers.

## The control plane

| Component | What it does |
|---|---|
| **API server** | The front door. Every client (kubectl, the other components, your CI) reads and writes the cluster's objects through it, and it checks who may do what. |
| **Datastore** | Where the objects live. Usually etcd; K3s with a single server uses SQLite by default. Only the API server talks to it. |
| **Scheduler** | Picks a node for every new pod that has none, from the pod's requests and constraints and the nodes' free room and labels. |
| **Controller manager** | Runs the controllers: loops that each watch one kind of object and act to make reality match it (Deployments, ReplicaSets, nodes, ...). |

## The nodes

| Component | What it does |
|---|---|
| **kubelet** | The node's agent. It reports the node's state, watches for pods assigned to its node, and has the container runtime start and stop their containers. |
| **Container runtime** | Pulls images and runs containers (containerd in K3s). |
| **kube-proxy** | Programs the node's network rules so that a Service's address reaches its pods. |

Notice that the scheduler never starts anything. It writes the chosen node into the pod object, and that node's
kubelet, which is watching through the API server, sees the pod and starts it. Every component works this way:
they never call each other, they all read and write objects through the API server.

## This lab's cluster

The lab runs **K3s**, a small Kubernetes distribution that packs the control plane into one process:

- **${node:a}** in region a is the **server**: the control plane, and a node that also runs pods.
- **${node:b}** and **${node:c}** in regions b and c are **agents**: workers that only run pods.

You reach the API server with `opsforge kubectl`, the usual `kubectl` already pointed at the lab's cluster:

```bash
opsforge kubectl get nodes -o wide
```
