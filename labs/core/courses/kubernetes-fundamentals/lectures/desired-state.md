---
title: The desired-state loop
minutes: 6
resources:
  - { title: "Kubernetes docs: Controllers", url: "https://kubernetes.io/docs/concepts/architecture/controller/" }
---
You do not tell Kubernetes *what to do*. You tell it *what you want*, and it works out what to do, over and over.

Every object has two parts:

- **`spec`**: what you want. "Three replicas of this pod template." You write it.
- **`status`**: what is true now. "Two replicas ready." The cluster writes it.

A **controller** is a loop that watches one kind of object and acts whenever `status` and `spec` disagree:

1. **Observe**: read the objects it is responsible for.
2. **Compare**: is what exists what was asked for?
3. **Act**: create, delete or change something to close the gap, then go back to 1.

## An example

You ask for a Deployment of three pods, and the node running one of them disappears.

- The node controller notices the node stopped reporting and marks it not ready.
- After a while, the pod on that node is evicted: it is marked for deletion.
- The ReplicaSet controller now counts two pods where its `spec` says three, and creates a new one.
- The scheduler sees a pod without a node and picks a healthy one.
- That node's kubelet starts the containers.

Nobody ran a recovery script. Each controller did its one small job, and the cluster drifted back to what you
declared. You will watch exactly this happen in the lab *Losing a site*.

> [!NOTE]
> Because controllers compare and act again and again, declaring something twice changes nothing, and a change made
> by hand that contradicts the `spec` is undone. If a pod you deleted comes back, a controller wanted it.
