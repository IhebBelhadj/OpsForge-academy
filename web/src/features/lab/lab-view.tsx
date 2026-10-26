import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { Check, ChevronDown, Lightbulb, Loader2, Play, RefreshCw, RotateCcw, Square } from "lucide-react";
import { type ReactNode, useEffect, useState } from "react";

import { actions, type Item, keys, type LabAttempt, type LabOutcome, type Operation, queries } from "@/api/client";
import { awaitOperation } from "@/api/events";
import { Lamp, type LampColour } from "@/components/lamp";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogClose,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogTitle,
} from "@/components/ui/dialog";
import { RadioGroup, RadioGroupItem } from "@/components/ui/radio-group";
import { ResizableHandle, ResizablePanel, ResizablePanelGroup } from "@/components/ui/resizable";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { labChanged, LiveLabMap } from "@/features/labmap";
import { Markdown } from "@/features/lecture";
import { TerminalInput, TerminalPane, type TerminalStatus } from "@/features/terminal";
import { useIsMobile } from "@/hooks/use-mobile";
import { count, duration } from "@/lib/format";
import { cn } from "@/lib/utils";

type Lab = NonNullable<Item["lab"]>;
type Step = Lab["steps"][number];
type Busy = "start" | "check" | "hint" | "reset" | "abandon";

/** What the header says the lab is doing. */
type Shown = "idle" | "setup" | "running" | "passed";
const shownName: Record<Shown, string> = {
  idle: "Not started",
  setup: "Setting up",
  running: "Running",
  passed: "Passed",
};
const shownLamp: Record<Shown, LampColour> = { idle: "off", setup: "amber", running: "green", passed: "green" };

const busyName: Record<Busy, string> = {
  start: "Setting up the lab…",
  check: "Checking on the lab…",
  hint: "Opening the hint…",
  reset: "Setting the lab up again…",
  abandon: "Tearing the lab down…",
};

/** A lab in the player (design.md §5, Killercoda's layout): the steps left, each checked on the lab before the next
 * opens; the terminal, the lab's files and the lab map right. One attempt runs in an OpsForge sandbox: starting,
 * checking, resetting and abandoning are operations (202, then their `operation` event); the attempt's steps, hints
 * and outcome come from the server. */
export function LabView({ item, number, next }: { item: Item; number: string; next?: ReactNode }) {
  const lab = item.lab;
  const client = useQueryClient();
  const mobile = useIsMobile();
  const current = useQuery(queries.currentAttempt());
  const labState = useQuery(queries.labState());
  const [mine, setMine] = useState<LabAttempt>();
  const [outcome, setOutcome] = useState<LabOutcome>();
  const [busy, setBusy] = useState<Busy>();
  const [error, setError] = useState<string>();
  const [tab, setTab] = useState("terminal");
  const [terminal, setTerminal] = useState<TerminalStatus>();
  const [type, setType] = useState<((text: string) => void) | null>(null);
  const [confirmAbandon, setConfirmAbandon] = useState(false);

  // The attempt shown: the one this page started or last read, else the learner's running attempt at this lab
  const running = current.data;
  const attempt = mine ?? (running && running.lab === lab?.id ? running : undefined);
  const other = running && running.lab !== lab?.id ? running : undefined;

  // An attempt found in setup (the page was reloaded during it): follow it until it is running
  const settingUp = attempt?.status === "setup" && !busy;
  useEffect(() => {
    if (!settingUp) return;
    const t = setInterval(() => {
      void actions.labAttempt(attempt.id).then((a) => {
        if (a.status !== "setup") setMine(a);
      });
    }, 2000);
    return () => {
      clearInterval(t);
    };
  }, [settingUp, attempt?.id]);

  if (!lab) return null;

  const shown: Shown =
    attempt?.status === "setup" || busy === "start"
      ? "setup"
      : attempt?.status === "running"
        ? "running"
        : attempt?.status === "passed"
          ? "passed"
          : "idle";
  const live = attempt?.status === "running";

  /** Runs one lab operation to its end, then reads the attempt again. */
  async function operate(kind: Busy, call: () => Promise<Operation>) {
    setBusy(kind);
    setError(undefined);
    try {
      const op = await awaitOperation(await call());
      if (op.status === "failed") setError(op.error ?? "The lab operation could not finish.");
      const out = op.result;
      if (out) {
        setOutcome(out);
        if (out.error) setError(out.error);
        setMine(await actions.labAttempt(out.attempt));
      }
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(undefined);
      void client.invalidateQueries({ queryKey: keys.currentAttempt });
      void client.invalidateQueries({ queryKey: keys.labState });
      void client.invalidateQueries({ queryKey: keys.course(item.course) });
    }
  }

  function start() {
    if (!lab) return;
    setOutcome(undefined);
    void operate("start", async () => {
      const started = await actions.startLab({ lab: lab.id, course: item.course, item: item.id });
      setMine(started.attempt);
      return started.operation;
    });
  }

  async function hint(step: Step) {
    if (!attempt) return;
    setBusy("hint");
    setError(undefined);
    try {
      await actions.openHint({ attempt: attempt.id, step: step.id });
      setMine(await actions.labAttempt(attempt.id));
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(undefined);
    }
  }

  const steps = (
    <div className="h-full overflow-auto bg-surface">
      <div className="p-5 sm:p-6">
        <p className="text-ink-soft">
          Lab {number}, {duration(item.minutes)}
        </p>
        <h1 id="item-title" className="mt-1 text-2xl font-semibold tracking-[-0.01em]">
          {item.title}
        </h1>
        <p className="mt-2 text-ink-soft">{lab.summary}</p>
        <details className="group mt-4 rounded-lg border border-rule/30" open={shown === "idle"}>
          <summary className="flex cursor-pointer items-center justify-between px-4 py-2 font-semibold">
            Briefing
            <ChevronDown aria-hidden className="size-4 transition-transform group-open:rotate-180" />
          </summary>
          <Markdown className="px-4 pb-3">{lab.briefing}</Markdown>
        </details>

        {error ? (
          <p role="alert" className="mt-4 rounded-md border border-red/40 bg-red-soft/40 p-3 text-sm">
            {error}
          </p>
        ) : null}

        {shown === "passed" && attempt ? (
          <Debrief attempt={attempt} next={next} onAgain={start} debrief={lab.debrief} />
        ) : (
          <ol aria-label="Steps" className="mt-6">
            {lab.steps.map((s, i) => {
              const state = attempt?.steps.find((x) => x.id === s.id);
              const currentId = live ? attempt.current_step : lab.steps[0]?.id;
              return (
                <StepRow
                  key={s.id}
                  step={s}
                  index={i}
                  last={i === lab.steps.length - 1}
                  state={state?.passed ? "passed" : s.id === currentId ? "current" : "waiting"}
                  live={live}
                  hints={live ? (state?.hints ?? []) : []}
                  outcome={outcome?.step === s.id ? outcome : undefined}
                  checking={busy === "check"}
                  busy={Boolean(busy)}
                  onHint={() => void hint(s)}
                  onCheck={(answer) => {
                    if (!attempt) return;
                    void operate("check", () =>
                      actions.checkStep({
                        attempt: attempt.id,
                        step: s.id,
                        ...(answer === undefined ? {} : { answer }),
                      }),
                    );
                  }}
                />
              );
            })}
          </ol>
        )}
      </div>
    </div>
  );

  const changed = labState.data ? labChanged(labState.data) : false;
  const side = (
    <Tabs value={tab} onValueChange={setTab} className="flex h-full flex-col gap-0 bg-term-bg">
      <div className="flex items-center justify-between gap-2 border-b border-rule bg-enamel pr-3">
        <TabsList variant="line" className="h-10 rounded-none bg-transparent px-1">
          <TabsTrigger value="terminal">Terminal</TabsTrigger>
          {lab.files.length ? <TabsTrigger value="files">Files</TabsTrigger> : null}
          <TabsTrigger value="map" className="gap-2">
            Lab map
            {changed ? (
              <>
                <Lamp on="amber" />
                <span className="sr-only">(the lab differs from its topology)</span>
              </>
            ) : null}
          </TabsTrigger>
        </TabsList>
        <span className="flex items-center gap-2 text-sm text-ink-soft">
          <Lamp on={terminal === "connected" ? "green" : terminal === "reconnecting" ? "amber" : "off"} />
          {terminal === "connected" ? "Connected" : terminal === "reconnecting" ? "Reconnecting" : "No session"}
        </span>
      </div>
      {/* Kept mounted while another tab shows: the shell stays connected */}
      <TabsContent value="terminal" forceMount className="relative min-h-0 data-[state=inactive]:hidden">
        {live ? (
          <TerminalPane
            workspace={attempt.workspace}
            className="absolute inset-0"
            onStatus={setTerminal}
            onInput={(fn) => {
              setType(() => fn);
            }}
          />
        ) : (
          <StartPanel
            lab={lab}
            shown={shown}
            busy={busy}
            other={other}
            failed={attempt?.status === "failed" ? (attempt.error ?? "The setup failed.") : undefined}
            onStart={start}
            onAbandonOther={() => {
              if (other) void operate("abandon", () => actions.abandonLab(other.id));
            }}
          />
        )}
      </TabsContent>
      {lab.files.length ? (
        <TabsContent value="files" className="min-h-0 bg-surface">
          {live ? (
            <Files workspace={attempt.workspace} />
          ) : (
            <p className="p-5 text-sm text-ink-soft">The lab's files appear here once it runs.</p>
          )}
        </TabsContent>
      ) : null}
      <TabsContent value="map" className="min-h-0 overflow-auto bg-surface p-5">
        <LiveLabMap />
      </TabsContent>
    </Tabs>
  );

  // Run in a command block types into this lab's terminal (and shows it)
  const run = type
    ? (text: string) => {
        setTab("terminal");
        type(text);
      }
    : null;

  return (
    <TerminalInput.Provider value={run}>
      <section aria-labelledby="item-title" className="flex h-full flex-col">
        <div className="flex flex-wrap items-center gap-x-5 gap-y-2 border-b border-rule bg-surface px-4 py-2 text-sm">
          <span className="flex items-center gap-2 font-semibold">
            <Lamp on={shownLamp[shown]} />
            {shownName[shown]}
          </span>
          <span className="text-ink-soft">
            {live && attempt.current_step
              ? `Step ${String(lab.steps.findIndex((s) => s.id === attempt.current_step) + 1)} of ${String(lab.steps.length)}`
              : count(lab.steps.length, "step")}
          </span>
          <span className="text-ink-soft">
            {lab.xp} XP{attempt?.hints_used ? `, ${count(attempt.hints_used, "hint")} used` : ""}
          </span>
          {busy ? (
            <span role="status" className="flex items-center gap-2 text-ink-soft">
              <Loader2 aria-hidden className="size-4 animate-spin motion-reduce:animate-none" />
              {busyName[busy]}
            </span>
          ) : null}
          {live ? (
            <span className="ml-auto flex gap-2">
              <Button
                variant="outline"
                size="sm"
                className="bg-surface"
                disabled={Boolean(busy)}
                onClick={() => {
                  setOutcome(undefined);
                  void operate("reset", () => actions.resetLab(attempt.id));
                }}
              >
                <RotateCcw aria-hidden /> Reset lab
              </Button>
              <Button
                variant="outline"
                size="sm"
                className="border-red/40 bg-surface text-red hover:bg-red-soft hover:text-red"
                disabled={Boolean(busy)}
                onClick={() => {
                  setConfirmAbandon(true);
                }}
              >
                <Square aria-hidden /> Abandon
              </Button>
            </span>
          ) : null}
        </div>
        <ResizablePanelGroup orientation={mobile ? "vertical" : "horizontal"} className="min-h-0 flex-1">
          <ResizablePanel defaultSize={mobile ? "55%" : "40%"} minSize="25%">
            {steps}
          </ResizablePanel>
          <ResizableHandle withHandle className="bg-rule" />
          <ResizablePanel defaultSize={mobile ? "45%" : "60%"} minSize="25%">
            {side}
          </ResizablePanel>
        </ResizablePanelGroup>

        <Dialog open={confirmAbandon} onOpenChange={setConfirmAbandon}>
          <DialogContent className="border border-rule bg-surface">
            <DialogTitle className="text-xl font-semibold">Abandon this lab?</DialogTitle>
            <DialogDescription className="text-ink-soft">
              The lab is torn down and every link healed. Steps you passed are not kept; you can start again any time.
            </DialogDescription>
            <DialogFooter>
              <DialogClose asChild>
                <Button variant="outline" className="bg-surface">
                  Keep going
                </Button>
              </DialogClose>
              <Button
                className="bg-red text-surface hover:bg-red/90"
                onClick={() => {
                  setConfirmAbandon(false);
                  setOutcome(undefined);
                  if (attempt) void operate("abandon", () => actions.abandonLab(attempt.id));
                }}
              >
                Abandon lab
              </Button>
            </DialogFooter>
          </DialogContent>
        </Dialog>
      </section>
    </TerminalInput.Provider>
  );
}

function StartPanel({
  lab,
  shown,
  busy,
  other,
  failed,
  onStart,
  onAbandonOther,
}: {
  lab: Lab;
  shown: Shown;
  busy: Busy | undefined;
  other: LabAttempt | undefined;
  failed: string | undefined;
  onStart: () => void;
  onAbandonOther: () => void;
}) {
  return (
    <div className="absolute inset-0 grid place-items-center overflow-auto p-6 text-term-fg">
      <div className="w-full max-w-md">
        {shown === "setup" ? (
          <>
            <h2 className="flex items-center gap-2 text-xl font-semibold">
              <Loader2 aria-hidden className="size-5 animate-spin motion-reduce:animate-none" /> Setting up
            </h2>
            <p className="mt-2 text-sm opacity-80">
              OpsForge is creating your sandbox{lab.namespace ? " and its namespace" : ""} and running the lab's setup.
              This takes a few seconds to a minute.
            </p>
          </>
        ) : other ? (
          <>
            <h2 className="text-xl font-semibold">Another lab is running</h2>
            <p className="mt-2 text-sm opacity-80">
              You have <span className="font-semibold">{other.lab}</span> open. One lab runs at a time: finish or
              abandon it first.
            </p>
            <div className="mt-6 flex flex-wrap gap-3">
              {other.course && other.item ? (
                <Button asChild className="bg-term-fg text-term-bg hover:bg-term-fg/90">
                  <Link to="/learn/$course/$item" params={{ course: other.course, item: other.item }}>
                    Go to it
                  </Link>
                </Button>
              ) : null}
              <Button
                variant="outline"
                className="bg-transparent text-term-fg"
                disabled={Boolean(busy)}
                onClick={onAbandonOther}
              >
                <Square aria-hidden /> Abandon it
              </Button>
            </div>
          </>
        ) : (
          <>
            <h2 className="text-xl font-semibold">Start the lab on your cluster</h2>
            <p className="mt-2 text-sm opacity-80">
              Setup gives the lab a sandbox{lab.namespace ? " with its own namespace" : ""} and applies its starting
              state. Finishing or abandoning always tears it down and heals every link.
            </p>
            <ul className="mt-4 space-y-1 text-sm">
              {lab.needs.length ? <li>Needs: {lab.needs.join(", ")}</li> : null}
              {lab.layout ? <li>Layout: {lab.layout}</li> : null}
              {lab.requires.length ? <li>Pass first: {lab.requires.join(", ")}</li> : null}
              {lab.cluster_changes.map((c) => (
                <li key={c}>Changes outside its namespace: {c}</li>
              ))}
            </ul>
            {failed ? (
              <p role="alert" className="mt-4 rounded-md border border-red/60 p-3 text-sm">
                The last setup failed: {failed}
              </p>
            ) : null}
            <Button
              size="lg"
              onClick={onStart}
              disabled={Boolean(busy)}
              className="mt-6 bg-term-fg text-term-bg hover:bg-term-fg/90"
            >
              <Play aria-hidden /> {failed ? "Try again" : "Start lab"}
            </Button>
          </>
        )}
      </div>
    </div>
  );
}

function StepRow({
  step,
  index,
  last,
  state,
  live,
  hints,
  outcome,
  checking,
  busy,
  onHint,
  onCheck,
}: {
  step: Step;
  index: number;
  last: boolean;
  state: "passed" | "current" | "waiting";
  live: boolean;
  hints: string[];
  outcome: LabOutcome | undefined;
  checking: boolean;
  busy: boolean;
  onHint: () => void;
  onCheck: (answer?: number) => void;
}) {
  const [choice, setChoice] = useState("");
  const quiz = step.check.type === "quiz";
  return (
    <li className="relative flex gap-4 pb-6">
      {last ? null : (
        <span
          aria-hidden
          className={cn("absolute top-8 bottom-0 left-[13px] w-0.5", state === "passed" ? "bg-green" : "bg-rule")}
        />
      )}
      <span
        className={cn(
          "relative z-10 grid size-7 shrink-0 place-items-center rounded-full border-2 text-xs font-semibold",
          state === "passed" && "border-green bg-green text-white",
          state === "current" && "station-current border-ember bg-ember text-white",
          state === "waiting" && "border-control bg-surface text-ink-soft",
        )}
      >
        {state === "passed" ? <Check aria-hidden className="size-3.5" strokeWidth={3.5} /> : index + 1}
      </span>
      <div className="min-w-0 flex-1">
        <h2 className={cn("pt-0.5 font-semibold", state === "waiting" && "text-ink-soft")}>
          {step.title}
          <span className="sr-only">, {state === "current" ? "current step" : state}</span>
        </h2>
        {state === "passed" && outcome?.passed && outcome.explain ? (
          <Markdown variant="compact" className="mt-2 text-sm text-ink-soft">
            {outcome.explain}
          </Markdown>
        ) : null}
        {state === "current" ? (
          <div className="mt-2">
            <Markdown>{step.body}</Markdown>
            {quiz ? (
              <fieldset className="mt-4">
                <legend className="font-semibold">{step.check.question}</legend>
                <RadioGroup value={choice} onValueChange={setChoice} aria-label="Your answer" className="mt-2 gap-2">
                  {(step.check.choices ?? []).map((c, i) => (
                    <label key={c} className="flex items-center gap-2">
                      <RadioGroupItem value={String(i)} className="border-control" /> {c}
                    </label>
                  ))}
                </RadioGroup>
              </fieldset>
            ) : null}

            {step.hints ? (
              <div className="mt-4 space-y-2">
                {hints.map((h, i) => (
                  <div key={h} className="flex gap-2 rounded-md border-l-4 border-amber bg-enamel/60 p-3 text-sm">
                    <Lightbulb aria-hidden className="mt-0.5 size-4 shrink-0 text-amber" />
                    <div>
                      <span className="font-semibold">Hint {i + 1}: </span>
                      <Markdown variant="compact" className="inline [&>p]:inline">
                        {h}
                      </Markdown>
                    </div>
                  </div>
                ))}
                {hints.length < step.hints && live ? (
                  <Button variant="link" className="h-auto px-0 text-ink-soft" disabled={busy} onClick={onHint}>
                    <Lightbulb aria-hidden /> Show hint {hints.length + 1} of {step.hints} (costs XP)
                  </Button>
                ) : null}
              </div>
            ) : null}

            {outcome?.passed === false ? <CheckResult outcome={outcome} quiz={quiz} /> : null}
            {live ? null : <p className="mt-4 text-sm text-ink-soft">Start the lab to check this step.</p>}

            <Button
              className="mt-4"
              onClick={() => {
                onCheck(quiz ? Number(choice) : undefined);
              }}
              disabled={!live || busy || (quiz && !choice)}
            >
              {checking ? <Loader2 aria-hidden className="animate-spin motion-reduce:animate-none" /> : null}
              {checking
                ? "Checking on the lab…"
                : step.check.type === "confirm"
                  ? (step.check.label ?? "I have done this")
                  : quiz
                    ? "Answer"
                    : "Check"}
            </Button>
          </div>
        ) : null}
      </div>
    </li>
  );
}

/** Phase 10's itemised result: each expectation with what was wanted and what the lab showed. */
function CheckResult({ outcome, quiz }: { outcome: LabOutcome; quiz: boolean }) {
  if (quiz || !outcome.results.length)
    return (
      <p role="alert" className="mt-4 rounded-md border-2 border-red px-3 py-2 text-sm font-semibold">
        {quiz ? "Not quite: read the step again and try another answer." : "Not yet: the lab does not match."}
      </p>
    );
  return (
    <div role="alert" className="mt-4 overflow-hidden rounded-md border-2 border-red">
      <p className="bg-red px-3 py-1.5 text-sm font-semibold text-surface">Not yet: the lab does not match</p>
      <table className="w-full text-sm">
        <thead className="text-left text-ink-soft">
          <tr>
            <th className="px-3 py-1.5 font-normal">Check</th>
            <th className="px-3 py-1.5 font-normal">Wanted</th>
            <th className="px-3 py-1.5 font-normal">Found</th>
          </tr>
        </thead>
        <tbody className="font-mono">
          {outcome.results.map((r) => (
            <tr key={r.metric} className="border-t border-rule">
              <td className="px-3 py-1.5">
                <span className="flex items-center gap-2">
                  <Lamp on={!r.checked ? "off" : r.passed ? "green" : "red"} /> {r.metric}
                </span>
              </td>
              <td className="px-3 py-1.5">{r.want}</td>
              <td className={cn("px-3 py-1.5", r.checked && !r.passed && "font-semibold text-red")}>
                {r.checked ? (r.got ?? "(none)") : "not reached"}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

function Debrief({
  attempt,
  next,
  onAgain,
  debrief,
}: {
  attempt: LabAttempt;
  next?: ReactNode;
  onAgain: () => void;
  debrief: string;
}) {
  return (
    <div role="status" className="mt-6 rounded-lg border border-rule bg-enamel p-5">
      <p className="flex items-center gap-2 text-ink-soft">
        <Lamp on="green" /> Every step passed. The lab is torn down and the links healed.
      </p>
      <h2 className="mt-1 text-2xl font-semibold tracking-[-0.01em]">Lab passed</h2>
      <dl className="mt-4 grid grid-cols-3 gap-3">
        {[
          ["XP", `+${String(attempt.xp ?? 0)}`],
          ["Time", attempt.seconds === null ? "–" : duration(Math.max(1, Math.round(attempt.seconds / 60)))],
          ["Hints", String(attempt.hints_used)],
        ].map(([t, v]) => (
          <div key={t} className="rounded-lg border border-rule bg-surface p-3">
            <dt className="text-sm text-ink-soft">{t}</dt>
            <dd className="text-xl font-semibold">{v}</dd>
          </div>
        ))}
      </dl>
      {debrief ? <Markdown className="mt-4">{debrief}</Markdown> : null}
      <div className="mt-5 flex flex-wrap gap-3">
        {next}
        <Button variant="outline" className="bg-surface" onClick={onAgain}>
          <RotateCcw aria-hidden /> Run it again
        </Button>
      </div>
    </div>
  );
}

/** The attempt's workspace, read-only: the lab's files as they are in the sandbox (edit them in the terminal; the
 * editor comes with M3). */
function Files({ workspace }: { workspace: string }) {
  const tree = useQuery(queries.workspaceTree(workspace));
  const [picked, setPicked] = useState<string>();
  const path = picked ?? tree.data?.files[0]?.path;
  const file = useQuery({ ...queries.workspaceFile(workspace, path ?? ""), enabled: Boolean(path) });
  return (
    <div className="flex h-full flex-col">
      <div className="flex items-center gap-1 overflow-x-auto border-b border-rule bg-enamel px-2">
        {(tree.data?.files ?? []).map((f) => (
          <button
            key={f.path}
            type="button"
            aria-pressed={f.path === path}
            onClick={() => {
              setPicked(f.path);
            }}
            className={cn(
              "border-b-2 px-3 py-2 font-mono text-sm",
              f.path === path ? "border-ink font-semibold" : "border-transparent text-ink-soft",
            )}
          >
            {f.path}
          </button>
        ))}
        <Button
          variant="ghost"
          size="sm"
          className="ml-auto"
          aria-label="Read the files again"
          onClick={() => void Promise.all([tree.refetch(), file.refetch()])}
        >
          <RefreshCw aria-hidden />
        </Button>
      </div>
      <pre className="min-h-0 flex-1 overflow-auto p-4 font-mono text-[0.84rem] leading-6">
        {file.data?.content ?? (tree.data && !tree.data.files.length ? "No files yet." : "")}
      </pre>
      <p className="border-t border-rule px-4 py-2 text-xs text-ink-soft">
        Read-only here: edit the files in the terminal. The editor comes later.
      </p>
    </div>
  );
}
