// Design-time runtime of lab sessions and challenge runs: what the server will send once those screens are hooked to
// labsession and challenge.

export const terminalTranscript = `lab OpsForge $ opsforge kubectl -n lab-7 get pods
NAME                   READY   STATUS             RESTARTS      AGE
web-6c9f8d7b5d-x2kqp   0/1     CrashLoopBackOff   4 (18s ago)   2m11s
lab OpsForge $ opsforge kubectl -n lab-7 logs web-6c9f8d7b5d-x2kqp
error: unknown flag: --listen
usage: whoami [--port PORT] [--name NAME]
lab OpsForge $ `;

export const setupLog = [
  "Checking the lab: up, stretched layout",
  "Creating namespace lab-7 on cluster lab",
  "Applying web.yaml (Deployment web, 1 replica)",
  "Waiting for the first restart",
  "Ready",
];

export interface MockSite {
  id: string;
  name: string;
  x: number;
  y: number;
  nodes: string[];
}

export const sites: MockSite[] = [
  { id: "a", name: "Region a", x: 110, y: 70, nodes: ["a-s1", "a-w1"] },
  { id: "b", name: "Region b", x: 490, y: 70, nodes: ["b-w1"] },
  { id: "c", name: "Region c", x: 300, y: 250, nodes: ["c-w1"] },
];

export const links: { from: string; to: string; rtt: string; state: "nominal" | "changed" | "cut" }[] = [
  { from: "a", to: "b", rtt: "40 ms", state: "nominal" },
  { from: "a", to: "c", rtt: "80 ms", state: "changed" },
  { from: "b", to: "c", rtt: "60 ms", state: "nominal" },
];

export interface Submission {
  at: string;
  verdict: "Accepted" | "Wrong answer" | "Invalid manifest" | "Time limit exceeded";
  runtime?: string;
}

export const submissions: Submission[] = [
  { at: "2026-10-24T17:22:00Z", verdict: "Invalid manifest" },
  { at: "2026-10-24T17:26:00Z", verdict: "Wrong answer", runtime: "6 s" },
];
