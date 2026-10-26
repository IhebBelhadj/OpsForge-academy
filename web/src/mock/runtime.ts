// Design-time runtime of challenge runs: what the server will send once the challenge screen is hooked to the
// challenge module (M3).

export interface Submission {
  at: string;
  verdict: "Accepted" | "Wrong answer" | "Invalid manifest" | "Time limit exceeded";
  runtime?: string;
}

export const submissions: Submission[] = [
  { at: "2026-10-24T17:22:00Z", verdict: "Invalid manifest" },
  { at: "2026-10-24T17:26:00Z", verdict: "Wrong answer", runtime: "6 s" },
];
