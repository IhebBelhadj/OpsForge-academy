---
title: Routing around failure
minutes: 8
resources:
  - { title: "Cloudflare: What is routing?", url: "https://www.cloudflare.com/learning/network-layer/what-is-routing/" }
---
Every router keeps a **routing table**: for each destination network, which interface or neighbour to send packets
to. Someone has to fill it in, and there are two ways.

## Static and dynamic routes

A **static route** is written by hand: "c's network: through the tunnel to c". It is simple and predictable, and
it keeps pointing at the tunnel after the tunnel dies. Packets are sent into a link that drops them, until someone
notices and edits the route.

A **routing protocol** fills the table in automatically. Routers talk to their neighbours, learn what each can
reach, and recompute their routes when something changes. Inside one organisation the classic choice is **OSPF**
(Open Shortest Path First); between organisations on the internet it is BGP.

## How OSPF chooses

1. **Neighbours.** Each router sends small *hello* packets on every link. While hellos keep arriving, the neighbour
   is up. When several are missed in a row, it is declared dead.
2. **A map.** Routers share which links they have and what each costs, so every router builds the same map of
   the whole network.
3. **Shortest paths.** Each router computes the cheapest path to every destination over that map, and installs
   the first hop in its routing table.

When a neighbour dies, its links leave the map, every router recomputes, and traffic takes the next cheapest path.
Nobody edits anything.

## Costs in this lab

The gateways run OSPF over their tunnels, and each tunnel's cost is its round-trip time in milliseconds:

| Path from a to c | Cost |
|---|---|
| direct, a → c | 120 |
| through b, a → b → c | 40 + 90 = 130 |

The direct tunnel wins while it works. Break it and a's gateway notices after a few missed hellos, about ten
seconds in this lab, then sends traffic for c through b. The detour costs 10 ms more per round trip, but the
regions can still talk.

> [!WARNING]
> Faster failure detection is not free. Declaring a neighbour dead after one missed hello would react to every
> lost packet, and routes that flip back and forth (**flapping**) hurt more than a slow failover.

In the next lab you find the direct path, drop every packet on it, and watch the route change.
