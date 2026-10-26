---
title: Regions, distance and round trips
minutes: 8
resources:
  - { title: "Cloudflare: What is round-trip time (RTT)?", url: "https://www.cloudflare.com/learning/cdn/glossary/round-trip-time-rtt/" }
---
A **region** is a cloud provider's group of data centres in one geographic area: Frankfurt, Virginia, Singapore.
You choose one for every machine, database and bucket you create, and the choice decides three things:

- **How far your users are.** Requests travel to the region and back.
- **What fails together.** A region shares power grids, networks and operators; an outage there takes everything
  in it down, so services that must survive one run in several.
- **Where data lives.** Laws often say a country's personal data must stay in that country.

## Distance costs time

Light in an optical fibre covers about **200 km per millisecond**. Frankfurt to New York is about 6,200 km in a
straight line, so a packet needs at least 31 ms one way, and the answer at least as long to come back. Cables do
not follow straight lines and every router on the way adds a little, so real numbers are higher. No amount of money
or bandwidth changes this: bandwidth is how *much* you can send at once, latency is how *long* each trip takes.

What tools report is usually the **round-trip time** (RTT): the time from sending a packet to receiving its answer.
`ping` prints it per packet and summarises it at the end:

```
rtt min/avg/max/mdev = 118.2/121.4/139.0/6.1 ms
```

`avg` is the number to plan with; `mdev` (the spread) is **jitter**, how much the trip time varies.

## Counting round trips

A single request is rarely a single round trip. Opening an HTTPS page from scratch needs:

1. one round trip for the TCP handshake,
2. one for the TLS 1.3 handshake,
3. one for the request and its answer.

At 120 ms RTT that is about 360 ms before the first byte of the page arrives, before the page asks for its images,
scripts and API calls. This is why services keep connections open, terminate TLS close to the user (a CDN), and put
copies of read-heavy data in every region.

> [!TIP]
> When you design anything that crosses regions, count its round trips first. A chatty protocol that is fine in
> one data centre can be unusable between continents.

## Regions in this lab

Sites **a**, **b** and **c** play three regions. Each has its own machines and its own gateway, and between the
gateways runs a simulated internet that adds delay, jitter and loss per link:

| Link | Round trip |
|---|---|
| a – b | 40 ms |
| b – c | 90 ms |
| a – c | 120 ms |

`opsforge link a-c` prints a link's current conditions, and you can change them live, which is what the next lab
does: you will move region c further away and measure what it costs.
