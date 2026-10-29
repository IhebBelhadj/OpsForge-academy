import type { LabOutcome, TutorConversation, TutorToolCall } from "@/api/client";

/** A tool call as the learner reads it: Academy's own tools by what they look at, a refused one by what it was. */
export function toolLabel(call: TutorToolCall): string {
  const t = call.title.toLowerCase();
  if (call.status === "denied") return `Refused: ${call.title || "a tool call"}`;
  if (t.includes("lab_attempt")) return "Read your steps";
  if (t.includes("terminal_output")) return "Read your terminal";
  if (t.includes("lab_state")) return "Looked at the lab";
  return call.title || "A tool call";
}

/** Whether the conversation is waiting on its agent (the page follows it until it is not). */
export function busy(c: TutorConversation | undefined) {
  return c?.status === "starting" || c?.status === "answering";
}

/** Whether the conversation has ended (a new one must be started). */
export function ended(c: TutorConversation | undefined) {
  return c?.status === "failed" || c?.status === "closed";
}

/** The question "Ask the tutor" sends for a failed check: the step and what the lab showed, so the tutor starts
 * from the evidence (it reads the rest with its tools). */
export function failedCheckQuestion(stepTitle: string, outcome: LabOutcome): string {
  const results = outcome.results
    .filter((r) => r.checked)
    .map((r) => `- ${r.metric}: wanted ${r.want}, the lab showed ${r.got === null ? "nothing" : String(r.got)}`)
    .join("\n");
  return (
    `My check of the step "${stepTitle}" failed.` +
    (results ? `\n\nWhat the check measured:\n${results}` : "") +
    (outcome.error ? `\n\nThe check said: ${outcome.error}` : "") +
    "\n\nWhat is wrong, and what should I look at? Don't give me the answer yet."
  );
}

/** Questions to start from. */
export const suggestions = [
  "What is this step asking me to do?",
  "Look at my terminal: what went wrong?",
  "Explain what the lab map shows right now.",
];

const agentKey = "academy.tutor.agent";

/** The agent the learner picked last (per browser). */
export function rememberedAgent(): string | undefined {
  try {
    return localStorage.getItem(agentKey) ?? undefined;
  } catch {
    return undefined;
  }
}

export function rememberAgent(id: string) {
  try {
    localStorage.setItem(agentKey, id);
  } catch {
    // private window: forget it
  }
}
