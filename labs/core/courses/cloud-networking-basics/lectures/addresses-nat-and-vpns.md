---
title: Private addresses, NAT and VPNs
minutes: 10
resources:
  - { title: "RFC 1918: Address allocation for private internets", url: "https://www.rfc-editor.org/rfc/rfc1918" }
  - { title: "WireGuard: conceptual overview", url: "https://www.wireguard.com/#conceptual-overview" }
---
Most machines in a cloud never get an address the internet can reach. They live on a **private network** (AWS and
Google call it a VPC, Azure a VNet) and a gateway decides what goes in and out.

## Private addresses

Three address ranges are reserved for private use (RFC 1918):

| Range | Size |
|---|---|
| `10.0.0.0/8` | 16 million addresses |
| `172.16.0.0/12` | 1 million addresses |
| `192.168.0.0/16` | 65,536 addresses |

Routers on the internet drop packets addressed to them, so the same `10.20.0.11` can exist in thousands of
networks at once. Inside one network it names exactly one machine; outside it means nothing.

## NAT: sharing one public address

A **public address** is unique on the whole internet. A site's gateway owns one and translates for the machines
behind it, which is **network address translation** (NAT):

- **Going out** (source NAT): a machine at `10.20.0.11` sends a request; the gateway rewrites the source to its own
  public address and remembers the connection, so the answer finds its way back. The server only ever sees the
  gateway.
- **Coming in** (destination NAT, or port forwarding): the gateway sends what arrives on a public port to one
  private machine. In this lab each site's gateway forwards ports 80 and 443 to the site's Kubernetes ingress.

Anything not forwarded cannot be reached from outside at all, which is the point: private by default.

## Site-to-site VPN: joining private networks

Two regions often need to talk privately: an application in one, its database in another. A **site-to-site VPN**
joins their private networks through a tunnel between the two gateways:

1. A machine in b sends a packet to a private address in c, as if c were next door.
2. b's gateway has a route saying "c's private network: through the tunnel to c". It encrypts the whole packet and
   wraps it in a new one, from b's public address to c's public address.
3. c's gateway unwraps it and delivers the original packet, private source address included.

The internet only sees encrypted packets between two public addresses. Inside the tunnel there is no translation,
so c sees who really sent the packet.

> [!NOTE]
> The lab's gateways use **WireGuard**, and each tunnel is an interface named after the site at the other end:
> on b's gateway, `wg-c` leads to c and `wg-a` to a.

In the next lab you find a node's private address, follow a packet through the tunnel, and compare what c sees
when the same request comes through the tunnel and over the public internet.
