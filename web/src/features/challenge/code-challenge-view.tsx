import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Check, Loader2, Lock, Play, RotateCcw, Send, X } from "lucide-react";
import { type ReactNode, useState } from "react";

import {
  actions,
  type ChallengeOutcome,
  type CodeLanguage,
  type Item,
  keys,
  ProblemError,
  queries,
  type Submission,
} from "@/api/client";
import { awaitOperation } from "@/api/events";
import { DifficultyLabel } from "@/components/difficulty";
import { Lamp } from "@/components/lamp";
import { Button } from "@/components/ui/button";
import { ResizableHandle, ResizablePanel, ResizablePanelGroup } from "@/components/ui/resizable";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { EditorSurface } from "@/features/editor";
import { Markdown } from "@/features/lecture";
import { useIsMobile } from "@/hooks/use-mobile";
import { duration } from "@/lib/format";
import { cn } from "@/lib/utils";

/** Each language's file, its label and how the challenge's function is named in it. */
type Challenge = NonNullable<Item["challenge"]>;
type Code = NonNullable<Challenge["code"]>;

const languages: Record<CodeLanguage, { file: string; label: string; name: (f: string) => string }> = {
  python: { file: "solution.py", label: "Python", name: (f) => f.replace(/[A-Z]/g, (c) => `_${c.toLowerCase()}`) },
  javascript: { file: "solution.js", label: "JavaScript", name: (f) => f },
  go: { file: "solution.go", label: "Go", name: (f) => f.charAt(0).toUpperCase() + f.slice(1) },
};

export const verdicts: Record<ChallengeOutcome["verdict"], { label: string; lamp: "green" | "red" | "amber" }> = {
  accepted: { label: "Accepted", lamp: "green" },
  wrong_answer: { label: "Wrong answer", lamp: "red" },
  runtime_error: { label: "Runtime error", lamp: "red" },
  compile_error: { label: "Compile error", lamp: "red" },
  time_limit_exceeded: { label: "Time limit exceeded", lamp: "amber" },
  memory_limit_exceeded: { label: "Memory limit exceeded", lamp: "amber" },
  error: { label: "Not judged", lamp: "amber" },
};

const json = (v: unknown) => JSON.stringify(v ?? null);

/** The learner's unsent code, per challenge and language, kept in this browser. */
function draftKey(item: Item, lang: CodeLanguage) {
  return `academy.code.${item.course}.${item.id}.${lang}`;
}
function loadDraft(item: Item, lang: CodeLanguage): string | undefined {
  try {
    return localStorage.getItem(draftKey(item, lang)) ?? undefined;
  } catch {
    return undefined;
  }
}
function saveDraft(item: Item, lang: CodeLanguage, code: string | undefined) {
  try {
    if (code === undefined) localStorage.removeItem(draftKey(item, lang));
    else localStorage.setItem(draftKey(item, lang), code);
  } catch {
    // private window or blocked storage: the draft lives as long as the page
  }
}

/** A coding challenge in the player (LeetCode's problem screen): the statement, solution and submissions left; the
 * code right, in one of the challenge's languages, with the examples and the result below it. Run tries the
 * examples, Submit every case; both are judged in a sandbox on the lab. */
export function CodeChallengeView({
  item,
  challenge,
  code,
  number,
  next,
}: {
  item: Item;
  challenge: Challenge;
  code: Code;
  number: string;
  next?: ReactNode;
}) {
  const mobile = useIsMobile();
  const client = useQueryClient();
  const [lang, setLang] = useState<CodeLanguage>(code.languages[0] ?? "python");
  const file = languages[lang].file;
  const starter = challenge.starter[file] ?? "";
  const [texts, setTexts] = useState<Partial<Record<CodeLanguage, string>>>(() =>
    Object.fromEntries(
      code.languages.map((l) => [l, loadDraft(item, l) ?? challenge.starter[languages[l].file] ?? ""]),
    ),
  );
  const text = texts[lang] ?? starter;
  const [pane, setPane] = useState("cases");
  const [left, setLeft] = useState("description");
  const [outcome, setOutcome] = useState<{ kind: "run" | "submit"; result: ChallengeOutcome }>();
  const [problem, setProblem] = useState<string>();

  const submissions = useQuery(queries.submissions(item.course, item.id));
  const accepted = (submissions.data ?? []).some((s) => s.verdict === "accepted");
  const solution = useQuery({ ...queries.solution(item.course, item.id), enabled: accepted });

  const judge = useMutation({
    mutationFn: async (kind: "run" | "submit") => {
      const call = kind === "run" ? actions.runCode : actions.submitCode;
      const op = await awaitOperation(await call({ course: item.course, item: item.id, language: lang, code: text }));
      if (op.status === "failed" || !op.result) throw new Error(op.error ?? "the judge failed");
      return { kind, result: op.result as ChallengeOutcome };
    },
    onMutate: () => {
      setPane("result");
      setProblem(undefined);
    },
    onSuccess: (o) => {
      setOutcome(o);
      if (o.kind === "submit") {
        void client.invalidateQueries({ queryKey: keys.submissions(item.course, item.id) });
        void client.invalidateQueries({ queryKey: keys.course(item.course) });
      }
    },
    onError: (err) => {
      setProblem(err instanceof ProblemError ? (err.problem.detail ?? err.problem.title) : String(err));
    },
  });

  function edit(value: string) {
    setTexts((t) => ({ ...t, [lang]: value }));
    saveDraft(item, lang, value === starter ? undefined : value);
  }

  const signature = `${languages[lang].name(code.function)}(${code.params.join(", ")})`;

  const description = (
    <Tabs value={left} onValueChange={setLeft} className="flex h-full flex-col gap-0 bg-surface">
      <TabsList variant="line" className="h-10 w-full justify-start rounded-none border-b border-rule bg-enamel px-1">
        <TabsTrigger value="description" className="flex-none">
          Description
        </TabsTrigger>
        <TabsTrigger value="solution" className="flex-none">
          {accepted ? null : <Lock aria-hidden />} Solution
        </TabsTrigger>
        <TabsTrigger value="submissions" className="flex-none">
          Submissions ({submissions.data?.length ?? 0})
        </TabsTrigger>
      </TabsList>
      <TabsContent value="description" className="min-h-0 overflow-auto p-5 sm:p-6">
        <p className="text-ink-soft">
          Challenge {number}, {duration(item.minutes)}
        </p>
        <h1 id="item-title" className="mt-1 text-2xl font-semibold tracking-[-0.01em]">
          {item.title}
        </h1>
        <p className="mt-3 flex flex-wrap items-center gap-x-4 gap-y-1 text-sm">
          <DifficultyLabel level={challenge.difficulty} />
          <span className="text-ink-soft">{challenge.xp} XP</span>
          <span className="text-ink-soft">
            {code.cases} test cases, {challenge.time_limit_s} s limit
          </span>
        </p>
        <Markdown className="mt-5">{challenge.statement}</Markdown>
        <p className="mt-6 text-sm text-ink-soft">
          Write <code className="font-mono text-ink">{signature}</code>
          {code.compare === "unordered" ? "; the answer may come in any order." : "."}
        </p>
      </TabsContent>
      <TabsContent value="solution" className="min-h-0 overflow-auto p-5 sm:p-6">
        {accepted && solution.data ? (
          <div>
            <p className="font-semibold">Reference solution in {languages[lang].label}</p>
            <pre className="mt-3 overflow-x-auto rounded-md bg-term-bg p-4 font-mono text-xs leading-5 text-term-fg">
              {solution.data[lang] ?? ""}
            </pre>
          </div>
        ) : (
          <div className="grid place-items-center gap-3 rounded-md border border-dashed border-control p-8 text-center">
            <Lock aria-hidden className="size-6 text-ink-soft" />
            <p className="font-semibold">Opens once a submission is accepted</p>
            <p className="max-w-[40ch] text-sm text-ink-soft">Then compare the reference solution with yours.</p>
          </div>
        )}
      </TabsContent>
      <TabsContent value="submissions" className="min-h-0 overflow-auto">
        <SubmissionTable rows={submissions.data ?? []} />
      </TabsContent>
    </Tabs>
  );

  const editor = (
    <div className="flex h-full flex-col">
      <div className="flex h-10 items-center justify-between gap-2 border-b border-rule bg-enamel px-2">
        <Select
          value={lang}
          onValueChange={(v) => {
            setLang(v as CodeLanguage);
          }}
        >
          <SelectTrigger size="sm" aria-label="Language" className="w-36 bg-surface">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            {code.languages.map((l) => (
              <SelectItem key={l} value={l}>
                {languages[l].label}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
        <Button
          variant="ghost"
          size="xs"
          onClick={() => {
            edit(starter);
          }}
          disabled={text === starter}
        >
          <RotateCcw aria-hidden /> Reset to starter
        </Button>
      </div>
      <EditorSurface
        path={file}
        text={text}
        open={[file]}
        vim={false}
        vimStatus={null}
        onEdit={(_, value) => {
          edit(value);
        }}
        onSave={() => {
          judge.mutate("run");
        }}
        className="flex-1"
      />
    </div>
  );

  const busy = judge.isPending ? judge.variables : null;
  const results = (
    <Tabs value={pane} onValueChange={setPane} className="flex h-full flex-col gap-0 bg-surface">
      <div className="flex items-center justify-between border-b border-rule bg-enamel pr-2">
        <TabsList variant="line" className="h-10 rounded-none bg-transparent px-1">
          <TabsTrigger value="cases">Test cases</TabsTrigger>
          <TabsTrigger value="result">Result</TabsTrigger>
        </TabsList>
        <span className="flex gap-2">
          <Button
            variant="outline"
            size="sm"
            className="bg-surface"
            disabled={busy !== null}
            onClick={() => {
              judge.mutate("run");
            }}
          >
            {busy === "run" ? (
              <Loader2 aria-hidden className="animate-spin motion-reduce:animate-none" />
            ) : (
              <Play aria-hidden />
            )}
            Run
          </Button>
          <Button
            size="sm"
            disabled={busy !== null}
            onClick={() => {
              judge.mutate("submit");
            }}
          >
            {busy === "submit" ? (
              <Loader2 aria-hidden className="animate-spin motion-reduce:animate-none" />
            ) : (
              <Send aria-hidden />
            )}
            Submit
          </Button>
        </span>
      </div>
      <TabsContent value="cases" className="min-h-0 overflow-auto p-4">
        <ol className="space-y-3">
          {code.examples.map((ex, i) => (
            <li key={i} className="rounded-md border border-rule bg-enamel/40 px-3 py-2 text-sm">
              <p className="font-semibold">Example {i + 1}</p>
              <Values params={code.params} args={ex.args} want={ex.want} />
            </li>
          ))}
        </ol>
      </TabsContent>
      <TabsContent value="result" className="min-h-0 overflow-auto p-4" aria-live="polite">
        {busy ? (
          <p className="flex items-center gap-2 text-ink-soft">
            <Loader2 aria-hidden className="size-4 animate-spin motion-reduce:animate-none" />
            {busy === "run" ? "Running the examples in the lab…" : "Judging every test case in the lab…"}
          </p>
        ) : problem ? (
          <p className="text-red">{problem}</p>
        ) : !outcome ? (
          <p className="text-ink-soft">Run tries the examples. Submit tries every test case and records the verdict.</p>
        ) : (
          <Outcome outcome={outcome.result} kind={outcome.kind} params={code.params} next={next} />
        )}
      </TabsContent>
    </Tabs>
  );

  return (
    <section aria-labelledby="item-title" className="h-full">
      <ResizablePanelGroup orientation={mobile ? "vertical" : "horizontal"} className="h-full">
        <ResizablePanel defaultSize="42%" minSize="25%">
          {description}
        </ResizablePanel>
        <ResizableHandle withHandle className="bg-rule" />
        <ResizablePanel defaultSize="58%" minSize="30%">
          <ResizablePanelGroup orientation="vertical">
            <ResizablePanel defaultSize="60%" minSize="25%">
              {editor}
            </ResizablePanel>
            <ResizableHandle withHandle className="bg-rule" />
            <ResizablePanel defaultSize="40%" minSize="15%">
              {results}
            </ResizablePanel>
          </ResizablePanelGroup>
        </ResizablePanel>
      </ResizablePanelGroup>
    </section>
  );
}

/** A case's arguments by parameter name, the expected answer and, once judged, what came back. */
function Values({
  params,
  args,
  want,
  got,
  error,
}: {
  params: string[];
  args: unknown[];
  want: unknown;
  got?: { value: unknown; ok: boolean };
  error?: string;
}) {
  return (
    <dl className="mt-1 grid grid-cols-[auto_1fr] gap-x-3 gap-y-0.5 font-mono text-xs">
      {params.map((p, i) => (
        <div key={p} className="contents">
          <dt className="text-ink-soft">{p} =</dt>
          <dd className="break-all">{json(args[i])}</dd>
        </div>
      ))}
      <dt className="text-ink-soft">Expected</dt>
      <dd className="break-all">{json(want)}</dd>
      {got ? (
        <>
          <dt className="text-ink-soft">Output</dt>
          <dd className={cn("break-all", got.ok ? "text-green" : "text-red")}>{json(got.value)}</dd>
        </>
      ) : null}
      {error ? (
        <>
          <dt className="text-ink-soft">Error</dt>
          <dd className="break-all whitespace-pre-wrap text-red">{error}</dd>
        </>
      ) : null}
    </dl>
  );
}

function Outcome({
  outcome,
  kind,
  params,
  next,
}: {
  outcome: ChallengeOutcome;
  kind: "run" | "submit";
  params: string[];
  next?: ReactNode;
}) {
  const v = verdicts[outcome.verdict];
  const good = outcome.verdict === "accepted";
  return (
    <div>
      <p className={cn("flex items-baseline gap-3 text-xl font-semibold", good ? "text-green" : "text-red")}>
        <span>{v.label}</span>
        <span className="text-sm font-normal text-ink-soft">
          {outcome.verdict === "error"
            ? outcome.error
            : `${String(outcome.passed)} / ${String(outcome.total)} ${kind === "run" ? "examples" : "test cases"} passed, ${outcome.seconds.toFixed(2)} s`}
        </span>
      </p>
      {kind === "submit" && good && outcome.xp > 0 ? <p className="mt-1 text-sm">+{outcome.xp} XP</p> : null}
      <ol className="mt-3 space-y-2">
        {outcome.cases.map((c) => (
          <li key={c.n} className="flex gap-2 text-sm">
            {c.passed ? (
              <Check aria-label="Passed" className="mt-0.5 size-4 shrink-0 text-green" strokeWidth={3} />
            ) : (
              <X aria-label="Failed" className="mt-0.5 size-4 shrink-0 text-red" strokeWidth={3} />
            )}
            <div className="min-w-0 flex-1">
              <span className="font-semibold">
                {kind === "run" ? "Example" : "Test case"} {c.n}
              </span>
              <Values
                params={params}
                args={c.args}
                want={c.want}
                {...(c.error ? { error: c.error } : { got: { value: c.got, ok: c.passed } })}
              />
            </div>
          </li>
        ))}
      </ol>
      {outcome.output ? (
        <>
          <p className="mt-4 text-sm font-semibold">{outcome.verdict === "compile_error" ? "Errors" : "Output"}</p>
          <pre className="mt-1 max-h-64 overflow-auto rounded-md bg-term-bg p-3 font-mono text-xs leading-5 text-term-fg">
            {outcome.output}
          </pre>
        </>
      ) : null}
      {kind === "submit" && good ? <div className="mt-4 flex gap-3">{next}</div> : null}
    </div>
  );
}

function SubmissionTable({ rows }: { rows: Submission[] }) {
  if (rows.length === 0) return <p className="p-5 text-sm text-ink-soft">No submissions yet.</p>;
  return (
    <table className="w-full text-sm">
      <thead className="border-b border-rule text-left text-ink-soft">
        <tr>
          <th className="px-5 py-2 font-normal">Verdict</th>
          <th className="px-5 py-2 font-normal">Language</th>
          <th className="px-5 py-2 font-normal">Passed</th>
          <th className="px-5 py-2 font-normal">When</th>
        </tr>
      </thead>
      <tbody>
        {rows.map((s) => (
          <tr key={s.id} className="border-b border-rule">
            <td className="px-5 py-2.5">
              <span
                className={cn(
                  "flex items-center gap-2 font-semibold",
                  s.verdict === "accepted" ? "text-green" : "text-red",
                )}
              >
                <Lamp on={verdicts[s.verdict].lamp} /> {verdicts[s.verdict].label}
              </span>
            </td>
            <td className="px-5 py-2.5">{languages[s.language].label}</td>
            <td className="px-5 py-2.5">
              {s.passed} / {s.total}
            </td>
            <td className="px-5 py-2.5 text-ink-soft">{when.format(new Date(s.submitted_at))}</td>
          </tr>
        ))}
      </tbody>
    </table>
  );
}

const when = new Intl.DateTimeFormat("en", { dateStyle: "medium", timeStyle: "short" });
