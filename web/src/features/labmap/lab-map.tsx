import { useQuery } from "@tanstack/react-query";

import { type LabState, queries } from "@/api/client";
import { Skeleton } from "@/components/ui/skeleton";
import { cn } from "@/lib/utils";

const linkClass = { ok: "stroke-ink", changed: "stroke-amber", cut: "stroke-red" } as const;
const linkName = { ok: "as configured", changed: "changed live", cut: "cut" } as const;

const W = 600;
const H = 340;
const BOX = 150;

/** Where each site's box goes: the lab's three regions as a triangle, any other count on a circle. */
function positions(ids: string[]): Record<string, { x: number; y: number }> {
  if (ids.length === 3) {
    const [a = "", b = "", c = ""] = ids;
    return { [a]: { x: 110, y: 80 }, [b]: { x: 490, y: 80 }, [c]: { x: 300, y: 250 } };
  }
  return Object.fromEntries(
    ids.map((id, i) => {
      const t = (2 * Math.PI * i) / Math.max(ids.length, 1) - Math.PI / 2;
      return [id, { x: W / 2 + 200 * Math.cos(t), y: H / 2 + 110 * Math.sin(t) }];
    }),
  );
}

/** "40ms" → "40 ms". */
function ms(rtt: string) {
  return rtt.replace(/^(\d+(?:\.\d+)?)(ms|s)$/, "$1 $2");
}

/** Whether the lab differs from its topology: a link changed or cut, a fault in place, a node not running. */
export function labChanged(state: LabState) {
  return (
    state.fault ||
    Object.values(state.links).some((l) => l.condition !== "ok") ||
    state.nodes.some((n) => n.status !== "Running")
  );
}

/** The mimic panel (design.md): the lab's sites and the WAN links between them, lamps for each link's state, as the
 * Phase 10 signal box drew them. Gateways are the site boxes themselves; the other machines are listed inside. */
export function LabMap({ state, className }: { state: LabState; className?: string | undefined }) {
  const ids = Object.keys(state.sites).sort();
  const at = positions(ids);
  const links = Object.entries(state.links).sort(([a], [b]) => a.localeCompare(b));
  const machines = (site: string) => state.nodes.filter((n) => n.site === site && n.role !== "gateway");
  return (
    <figure className={cn("flex flex-col", className)}>
      <svg
        viewBox={`0 0 ${String(W)} ${String(H)}`}
        className="h-auto w-full"
        role="img"
        aria-labelledby="labmap-title"
      >
        <title id="labmap-title">
          {`Lab map: ${links.map(([id, l]) => `link ${id} ${linkName[l.condition]}, ${ms(l.live.rtt)}`).join("; ")}`}
        </title>
        <rect width={W} height={H} rx={6} className="fill-enamel" />
        {links.map(([id, l]) => {
          const [from = "", to = ""] = id.split("-");
          const a = at[from];
          const b = at[to];
          if (!a || !b) return null;
          const mx = (a.x + b.x) / 2;
          const my = (a.y + b.y) / 2;
          return (
            <g key={id}>
              <line
                x1={a.x}
                y1={a.y}
                x2={b.x}
                y2={b.y}
                className={linkClass[l.condition]}
                strokeWidth={7}
                strokeLinecap="round"
                strokeDasharray={l.condition === "cut" ? "4 14" : undefined}
              />
              <rect
                x={mx - 38}
                y={my - 13}
                width={76}
                height={26}
                rx={13}
                className="fill-paper stroke-ink"
                strokeWidth={2}
              />
              <text x={mx} y={my + 5} textAnchor="middle" className="fill-ink font-sans text-[13px] font-semibold">
                {l.condition === "cut" ? "cut" : ms(l.live.rtt)}
              </text>
            </g>
          );
        })}
        {ids.map((id) => {
          const p = at[id];
          if (!p) return null;
          const nodes = machines(id);
          const h = 40 + 18 * Math.max(nodes.length, 1);
          return (
            <g key={id}>
              <rect
                x={p.x - BOX / 2}
                y={p.y - h / 2}
                width={BOX}
                height={h}
                rx={6}
                className="fill-paper stroke-ink"
                strokeWidth={3}
              />
              <text
                x={p.x - BOX / 2 + 14}
                y={p.y - h / 2 + 22}
                className="fill-ink font-sans text-[14px] font-semibold"
              >
                Region {id}
              </text>
              {nodes.map((n, i) => {
                const y = p.y - h / 2 + 42 + i * 18;
                return (
                  <g key={n.name}>
                    <circle
                      cx={p.x - BOX / 2 + 20}
                      cy={y - 4}
                      r={5}
                      className={cn(n.status === "Running" ? "fill-green" : "fill-red", "stroke-ink")}
                      strokeWidth={1.5}
                    />
                    <text x={p.x - BOX / 2 + 32} y={y} className="fill-ink-soft font-mono text-[12px]">
                      {n.name}
                      {n.status === "Running" ? "" : ` (${n.status})`}
                    </text>
                  </g>
                );
              })}
            </g>
          );
        })}
      </svg>
      <figcaption className="mt-3 flex flex-wrap gap-x-5 gap-y-1 text-sm text-ink-soft">
        <span className="flex items-center gap-2">
          <span aria-hidden className="h-1.5 w-6 rounded-full bg-ink" /> As configured
        </span>
        <span className="flex items-center gap-2">
          <span aria-hidden className="h-1.5 w-6 rounded-full bg-amber" /> Changed live
        </span>
        <span className="flex items-center gap-2">
          <span aria-hidden className="h-1.5 w-6 rounded-full border-t-4 border-dotted border-red" /> Cut
        </span>
        <span className="flex items-center gap-2">
          <span aria-hidden className="size-2.5 rounded-full bg-green" /> Machine running
        </span>
      </figcaption>
    </figure>
  );
}

/** The lab map of the lab as it is now (GET /lab/state, refreshed while shown). */
export function LiveLabMap({ className }: { className?: string }) {
  const state = useQuery({ ...queries.labState(), refetchInterval: 10_000 });
  if (state.isPending) return <Skeleton aria-label="Loading the lab map" className="aspect-[600/340] w-full" />;
  if (state.isError)
    return <p className="text-sm text-ink-soft">The lab map is not available: {state.error.message}</p>;
  if (!state.data.up)
    return (
      <p className="text-sm text-ink-soft">
        The lab is down{state.data.error ? `: ${state.data.error}` : ""}. Start it on the lab host, then come back.
      </p>
    );
  return <LabMap state={state.data} className={className} />;
}
