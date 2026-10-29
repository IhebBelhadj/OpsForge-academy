import { useQuery } from "@tanstack/react-query";

import { queries } from "@/api/client";
import { cn } from "@/lib/utils";

type Lamp = "off" | "green" | "amber" | "red";

const lampClass: Record<Lamp, string> = {
  off: "bg-lamp-off",
  green: "bg-green",
  amber: "bg-amber",
  red: "bg-red",
};

/** The platform's own lamp: green when every check passed, red with the reason when a required one failed, amber
 * when only OpsForge is out of reach (the courses work, labs do not). */
export function StatusLamp() {
  const query = useQuery(queries.readiness());

  let lamp: Lamp = "off";
  let label = "Checking the platform…";
  let detail: string | undefined;
  if (query.isError) {
    lamp = "red";
    label = "Platform unreachable";
  } else if (query.data) {
    const failed = query.data.checks.filter((c) => !c.ok);
    const reasons = failed.map((c) => `${c.name}: ${c.detail ?? "failed"}`).join("; ");
    if (failed.some((c) => c.required)) {
      lamp = "red";
      label = `${failed.map((c) => c.name).join(", ")} unavailable`;
      detail = reasons;
    } else if (failed.length) {
      lamp = "amber";
      label = "Labs offline";
      detail = `Labs need OpsForge. ${reasons}`;
    } else {
      lamp = "green";
      label = "Platform ready";
    }
  }

  return (
    <span
      role="status"
      data-lamp={lamp}
      {...(detail ? { title: detail } : {})}
      className="inline-flex items-center gap-2 text-sm text-ink-soft"
    >
      <span aria-hidden className={cn("size-2 rounded-full", lampClass[lamp])} />
      {label}
    </span>
  );
}
