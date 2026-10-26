---
title: Heartbeats, taints and eviction
minutes: 8
resources:
  - { title: "Kubernetes docs: Taint-based evictions", url: "https://kubernetes.io/docs/concepts/scheduling-eviction/taint-and-toleration/#taint-based-evictions" }
  - { title: "Kubernetes docs: Node heartbeats", url: "https://kubernetes.io/docs/concepts/architecture/nodes/#node-heartbeats" }
---
From the control plane, a node that crashed and a node cut off by a broken network look the same: it stopped
reporting. Kubernetes cannot tell them apart, and that shapes everything it does when a region goes dark.

## Heartbeats

Every kubelet tells the API server it is alive every few seconds. When the heartbeats stop for longer than a grace
period (well under a minute), the node controller sets the node's `Ready` condition to **`Unknown`** and the node
shows **`NotReady`**.

## Taints instead of instant deletion

Next, the node controller puts a **taint** on the node: `node.kubernetes.io/unreachable` with the effect
`NoExecute`. A taint repels pods; `NoExecute` also evicts the ones already there, unless they **tolerate** it.

Every pod gets a toleration for that taint automatically, with a time limit (`tolerationSeconds`, 300 seconds by
default; the lab shortens it to 60). So pods on the lost node:

1. keep their place while the toleration lasts, in case the node comes back after a short network blip;
2. are then evicted, and their controllers create replacements on reachable nodes;
3. stay listed as **`Terminating`**, because nobody can reach the node to confirm they stopped.

Waiting is deliberate. Replacing pods too soon could run two copies of something that must run once: the old pods
may well still be running on the cut-off node.

> [!NOTE]
> The time limit is a trade-off. Short: faster failover, but every network hiccup moves work around. Long: stable,
> but a region's pods stay unavailable for minutes. A pod can set its own `tolerationSeconds` to choose.

## After the region returns

When the node reports again it becomes `Ready`, the taint is removed, and the old pods are finally cleaned up. Their
replacements stay where they are: spread constraints act only when pods are placed, so the Deployment remains
lopsided until it is rolled out again.

In the next lab you partition region c and watch every stage.
