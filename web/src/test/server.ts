import { http, HttpResponse } from "msw";
import { setupServer } from "msw/node";

import type { components } from "@/api/schema";

type Schemas = components["schemas"];

/** Spec-shaped fixtures (typed by the generated schema, so they break when the contract does). The course is the
 * golden demo pack's (academy/testdata/packs/demo), as the platform serves it. */
export const fixtures = {
  ready: {
    status: "ready",
    checks: [
      { name: "database", ok: true },
      { name: "toolchain", ok: true },
    ],
  } satisfies Schemas["Readiness"],
  toolchainDown: {
    status: "unavailable",
    checks: [
      { name: "database", ok: true },
      { name: "toolchain", ok: false, detail: "toolchain speaks contract 2, academy speaks 1" },
    ],
  } satisfies Schemas["Readiness"],
  me: { user: { id: 1, name: "learner" }, csrf: "csrf-token" } satisfies Schemas["Me"],
  course: {
    id: "k8s-intro",
    pack: "demo",
    title: "Kubernetes introduction",
    subtitle: "One of everything, for the compiler's golden test.",
    level: "beginner",
    minutes: 33,
    needs: ["k3s"],
    requires: [],
    layout: null,
    badge: "intro-done",
    items: 4,
    section_count: 1,
    item_counts: { lecture: 1, quiz: 1, lab: 1, challenge: 1 },
    description: "A **tiny** course.\n",
    outcomes: ["Read a pod's status", "Write a pod manifest"],
    requirements: ["Basic shell use"],
    content_hash: "72236239ee5cafca",
    sections: [
      {
        title: "Pods",
        layout: null,
        needs: [],
        minutes: 33,
        items: [
          {
            id: "what-is-a-pod",
            type: "lecture",
            title: "What is a pod",
            minutes: 8,
            content_hash: "18e3d0e07448638c",
          },
          { id: "pods-check", type: "quiz", title: "Pods check", minutes: 5, content_hash: "61c63c2c869f2c32" },
          {
            id: "meet-the-cluster",
            type: "lab",
            title: "Meet the cluster",
            minutes: 10,
            content_hash: "80ddf88a45f08cbd",
          },
          { id: "first-pod", type: "challenge", title: "First pod", minutes: 10, content_hash: "40ad70fc8ed45ba6" },
        ],
      },
    ],
  } satisfies Schemas["Course"],
  items: {
    "what-is-a-pod": {
      course: "k8s-intro",
      id: "what-is-a-pod",
      type: "lecture",
      title: "What is a pod",
      minutes: 8,
      section: 0,
      content_hash: "18e3d0e07448638c",
      lecture: {
        markdown:
          "A pod is one or more containers that share a network namespace.\n\n![A pod](../../../assets/pod.svg)\n\n> [!NOTE]\n> Pods on `${node:c}` run in site c.\n",
        resources: [{ title: "Kubernetes docs: Pods", url: "https://kubernetes.io/docs/concepts/workloads/pods/" }],
      },
    },
    "pods-check": {
      course: "k8s-intro",
      id: "pods-check",
      type: "quiz",
      title: "Pods check",
      minutes: 5,
      section: 0,
      content_hash: "61c63c2c869f2c32",
      quiz: { pass: 70, xp: 20, time_limit_s: null, questions: 2 },
    },
    "meet-the-cluster": {
      course: "k8s-intro",
      id: "meet-the-cluster",
      type: "lab",
      title: "Meet the cluster",
      minutes: 10,
      section: 0,
      content_hash: "80ddf88a45f08cbd",
      lab: {
        id: "meet-the-cluster",
        summary: "List the nodes and find which site each one is in.",
        xp: 100,
        needs: ["k3s"],
        requires: [],
        layout: null,
        briefing: "The cluster spans three sites.\n",
        debrief: "",
        concepts: [],
        cluster_changes: [],
        namespace: true,
        files: ["hello.yaml"],
        steps: [
          {
            id: "nodes",
            title: "List the nodes",
            body: "```bash\nopsforge kubectl get nodes\n```\n",
            check: { type: "probe" },
            hints: 1,
          },
          {
            id: "apply",
            title: "Apply a manifest",
            body: "Apply `hello.yaml` in `${ns}`.",
            open: "hello.yaml",
            check: { type: "confirm", label: "Applied" },
            hints: 0,
          },
        ],
      },
    },
    "first-pod": {
      course: "k8s-intro",
      id: "first-pod",
      type: "challenge",
      title: "First pod",
      minutes: 10,
      section: 0,
      content_hash: "40ad70fc8ed45ba6",
      challenge: {
        difficulty: "easy",
        xp: 50,
        needs: ["k3s"],
        statement: "Run one pod named `web` from the image `traefik/whoami`.\n",
        files: ["pod.yaml"],
        starter: { "pod.yaml": "apiVersion: v1\nkind: Pod\nmetadata:\n  name: web\n" },
        time_limit_s: 60,
        tests: ["pod is ready"],
        hints: 1,
      },
    },
  } satisfies Record<string, Schemas["Item"]>,
};

/** The demo quiz "Pods check" as the server keeps it: keys and explanations reach the page only in a result. */
const podsCheck = [
  {
    id: "shared",
    type: "single" as const,
    prompt: "What do the containers of one pod share?",
    choices: ["Nothing", "A network namespace", "A node pool"],
    answer: [1],
    explain: "Containers in a pod share one network namespace, so they reach each other on localhost.",
    see: "what-is-a-pod",
  },
  {
    id: "phases",
    type: "multiple" as const,
    prompt: "Which of these are pod phases?",
    choices: ["Pending", "Running", "Sleeping", "Succeeded"],
    answer: [0, 1, 3],
    explain: "Sleeping is not a phase.",
    see: null,
  },
];

/** The learner's quiz attempts, newest last; tests start with none. */
let quizAttempts: Schemas["QuizAttempt"][] = [];
export function resetQuizAttempts() {
  quizAttempts = [];
}

function startQuizAttempt(timeLimitS?: number): Schemas["QuizAttempt"] {
  const now = Date.now();
  const attempt: Schemas["QuizAttempt"] = {
    id: quizAttempts.length + 1,
    course: "k8s-intro",
    item: "pods-check",
    started_at: new Date(now).toISOString(),
    deadline: timeLimitS ? new Date(now + timeLimitS * 1000).toISOString() : null,
    submitted_at: null,
    pass: 70,
    // The choices of the second question come reversed, as a shuffle would: answers name them by index
    questions: podsCheck.map((q, i) => {
      const choices = q.choices.map((text, index) => ({ index, text }));
      return { id: q.id, type: q.type, prompt: q.prompt, choices: i === 1 ? choices.reverse() : choices };
    }),
    result: null,
  };
  quizAttempts.push(attempt);
  return attempt;
}

/** Scores answers as the quiz module does: a left-out question is wrong, multiple choice is all or nothing. */
function scoreQuizAttempt(attempt: Schemas["QuizAttempt"], answers: Record<string, number[]>) {
  const firstPass = !quizAttempts.some((a) => a.result?.passed);
  const questions = podsCheck.map((q) => {
    const given = [...(answers[q.id] ?? [])].sort((a, b) => a - b);
    const correct = given.length === q.answer.length && given.every((v, i) => v === q.answer[i]);
    return { id: q.id, correct, answer: q.answer, given, explain: q.explain, see: q.see };
  });
  const correct = questions.filter((q) => q.correct).length;
  const score = Math.floor((correct * 100) / questions.length);
  const passed = score >= attempt.pass;
  attempt.submitted_at = new Date().toISOString();
  attempt.result = { score, passed, correct, total: questions.length, xp: passed && firstPass ? 20 : 0, questions };
  return attempt;
}

/** The lab as the platform runs it for the demo lab "Meet the cluster": its probe step fails on the first check and
 * passes on the next, its confirm step always passes; every operation is done when first asked for. */
const meetTheCluster = { steps: ["nodes", "apply"], hints: { nodes: ["Nodes are listed with `get nodes`."] } };

let labAttempts: Schemas["LabAttempt"][] = [];
let operations: Record<string, Schemas["Operation"]> = {};
let probes: Record<string, number> = {};
export function resetLab() {
  labAttempts = [];
  operations = {};
  probes = {};
}

export function labAttempt(over: Partial<Schemas["LabAttempt"]> = {}): Schemas["LabAttempt"] {
  const id = labAttempts.length + 1;
  const attempt: Schemas["LabAttempt"] = {
    id,
    lab: "meet-the-cluster",
    course: "k8s-intro",
    item: "meet-the-cluster",
    status: "running",
    started_at: new Date().toISOString(),
    finished_at: null,
    namespace: `sb-${String(id)}`,
    workspace: `sb-${String(id)}`,
    hints_used: 0,
    xp: null,
    seconds: null,
    error: null,
    current_step: "nodes",
    steps: meetTheCluster.steps.map((step) => ({ id: step, passed: false, hints: [] })),
    ...over,
  };
  labAttempts.push(attempt);
  return attempt;
}

function outcome(attempt: Schemas["LabAttempt"], over: Partial<Schemas["LabOutcome"]> = {}): Schemas["LabOutcome"] {
  return {
    attempt: attempt.id,
    status: attempt.status,
    step: null,
    passed: null,
    explain: null,
    results: [],
    xp: null,
    seconds: null,
    error: null,
    ...over,
  };
}

/** An operation answered running (202); `finish` runs when it is first read, as if it ended in between. */
const finishers: Record<string, () => Schemas["LabOutcome"]> = {};
function operation(kind: Schemas["Operation"]["kind"], finish: () => Schemas["LabOutcome"]) {
  const id = `op-${String(Object.keys(operations).length + 1)}`;
  const op: Schemas["Operation"] = {
    id,
    kind,
    subject: "meet-the-cluster",
    status: "running",
    result: null,
    error: null,
    created_at: new Date().toISOString(),
    finished_at: null,
  };
  operations[id] = op;
  finishers[id] = finish;
  return op;
}

export const labStateFixture = {
  up: true,
  error: null,
  topology: "topology.yml",
  layout: "stretched",
  fault: true,
  sites: {
    a: { public_ip: "198.51.100.1", vpc: "10.10.0.0/24" },
    b: { public_ip: "198.51.100.2", vpc: "10.20.0.0/24" },
    c: { public_ip: "198.51.100.3", vpc: "10.30.0.0/24" },
  },
  links: {
    "a-b": { condition: "ok", live: { rtt: "40ms" }, desired: { rtt: "40ms" } },
    "a-c": { condition: "cut", live: { rtt: "120ms", loss: "100%" }, desired: { rtt: "120ms", loss: "1%" } },
    "b-c": { condition: "changed", live: { rtt: "200ms" }, desired: { rtt: "90ms" } },
  },
  nodes: [
    { name: "a-cp1", site: "a", role: "k3s-server", status: "Running" },
    { name: "b-w1", site: "b", role: "k3s-agent", status: "Running" },
    { name: "c-w1", site: "c", role: "k3s-agent", status: "Stopped" },
    { name: "a-gw", site: "a", role: "gateway", status: "Running" },
  ],
} satisfies Schemas["LabState"];

export function notFound(detail: string): Schemas["Problem"] {
  return { type: "/problems/not-found", title: "Not found", status: 404, detail };
}

const problem = { headers: { "Content-Type": "application/problem+json" } };

/** The platform as the tests see it; a test overrides a handler with server.use(...). */
export const server = setupServer(
  http.get("*/healthz", () => HttpResponse.json({ status: "ok", version: "test" })),
  http.get("*/readyz", () => HttpResponse.json(fixtures.ready)),
  http.get("*/api/v1/me", () => HttpResponse.json(fixtures.me)),
  http.post("*/api/v1/sessions", () => HttpResponse.json(fixtures.me, { status: 201 })),
  http.get("*/api/v1/courses", () => {
    const {
      description: _d,
      outcomes: _o,
      requirements: _r,
      content_hash: _h,
      sections: _s,
      ...summary
    } = fixtures.course;
    return HttpResponse.json({ courses: [summary] });
  }),
  http.get("*/api/v1/courses/:course", ({ params }) =>
    params.course === fixtures.course.id
      ? HttpResponse.json(fixtures.course)
      : HttpResponse.json(notFound(`course ${String(params.course)} is not in the catalogue`), {
          status: 404,
          ...problem,
        }),
  ),
  http.get("*/api/v1/courses/:course/items/:item/quiz-attempts", () =>
    HttpResponse.json({
      attempts: quizAttempts
        .map((a) => ({
          id: a.id,
          started_at: a.started_at,
          submitted_at: a.submitted_at,
          score: a.result?.score ?? null,
          passed: a.result?.passed ?? null,
          xp: a.result?.xp ?? 0,
        }))
        .reverse(),
    }),
  ),
  http.post("*/api/v1/courses/:course/items/:item/quiz-attempts", () =>
    HttpResponse.json(startQuizAttempt(), { status: 201 }),
  ),
  http.get("*/api/v1/quiz-attempts/:attempt", ({ params }) => {
    const attempt = quizAttempts.find((a) => a.id === Number(params.attempt));
    return attempt
      ? HttpResponse.json(attempt)
      : HttpResponse.json(notFound("no such attempt"), { status: 404, ...problem });
  }),
  http.post("*/api/v1/quiz-attempts/:attempt/submit", async ({ params, request }) => {
    const attempt = quizAttempts.find((a) => a.id === Number(params.attempt));
    if (!attempt) return HttpResponse.json(notFound("no such attempt"), { status: 404, ...problem });
    if (attempt.submitted_at)
      return HttpResponse.json(
        { type: "/problems/already-submitted", title: "Already submitted", status: 409 },
        { status: 409, ...problem },
      );
    const { answers } = (await request.json()) as Schemas["QuizAnswers"];
    return HttpResponse.json(scoreQuizAttempt(attempt, answers));
  }),
  http.get("*/api/v1/attempts/current", () =>
    HttpResponse.json({ attempt: labAttempts.find((a) => a.status === "setup" || a.status === "running") ?? null }),
  ),
  http.get("*/api/v1/attempts/:attempt", ({ params }) => {
    const attempt = labAttempts.find((a) => a.id === Number(params.attempt));
    return attempt
      ? HttpResponse.json(attempt)
      : HttpResponse.json(notFound("no such attempt"), { status: 404, ...problem });
  }),
  http.post("*/api/v1/labs/:lab/attempts", () => {
    if (labAttempts.some((a) => a.status === "setup" || a.status === "running"))
      return HttpResponse.json(
        { type: "/problems/another-lab-running", title: "Another lab is running", status: 409 },
        { status: 409, ...problem },
      );
    const attempt = labAttempt({ status: "setup" });
    const op = operation("lab.start", () => {
      attempt.status = "running";
      return outcome(attempt);
    });
    return HttpResponse.json({ attempt, operation: op }, { status: 202 });
  }),
  http.post("*/api/v1/attempts/:attempt/checks", async ({ params, request }) => {
    const attempt = labAttempts.find((a) => a.id === Number(params.attempt));
    if (!attempt) return HttpResponse.json(notFound("no such attempt"), { status: 404, ...problem });
    const { step } = (await request.json()) as Schemas["StepCheckRequest"];
    const op = operation("lab.check", () => {
      const tries = (probes[step] = (probes[step] ?? 0) + 1);
      const passed = step !== "nodes" || tries > 1;
      if (!passed)
        return outcome(attempt, {
          step,
          passed,
          results: [{ metric: "nodes.value", want: "== 3", got: null, passed: false, checked: true }],
        });
      const state = attempt.steps.find((x) => x.id === step);
      if (state) state.passed = true;
      const next = attempt.steps.find((x) => !x.passed);
      attempt.current_step = next?.id ?? null;
      if (!next) {
        attempt.status = "passed";
        attempt.xp = 90;
        attempt.seconds = 420;
      }
      return outcome(attempt, { step, passed, ...(next ? {} : { xp: 90, seconds: 420 }) });
    });
    return HttpResponse.json(op, { status: 202 });
  }),
  http.post("*/api/v1/attempts/:attempt/hints", async ({ params, request }) => {
    const attempt = labAttempts.find((a) => a.id === Number(params.attempt));
    const { step } = (await request.json()) as Schemas["HintRequest"];
    const state = attempt?.steps.find((x) => x.id === step);
    const hint = (meetTheCluster.hints as Record<string, string[] | undefined>)[step]?.[state?.hints.length ?? 0];
    if (!attempt || !state || hint === undefined)
      return HttpResponse.json(notFound("no such hint"), { status: 404, ...problem });
    state.hints.push(hint);
    attempt.hints_used += 1;
    return HttpResponse.json({ step, index: state.hints.length - 1, hint, hints_used: attempt.hints_used });
  }),
  http.post("*/api/v1/attempts/:attempt/:action", ({ params }) => {
    const attempt = labAttempts.find((a) => a.id === Number(params.attempt));
    if (!attempt) return HttpResponse.json(notFound("no such attempt"), { status: 404, ...problem });
    const reset = params.action === "reset";
    const op = operation(reset ? "lab.reset" : "lab.abandon", () => {
      if (reset) {
        attempt.steps.forEach((x) => (x.passed = false));
        attempt.current_step = "nodes";
      } else attempt.status = "abandoned";
      return outcome(attempt);
    });
    return HttpResponse.json(op, { status: 202 });
  }),
  http.get("*/api/v1/operations/:operation", ({ params }) => {
    const op = operations[String(params.operation)];
    if (!op) return HttpResponse.json(notFound("no such operation"), { status: 404, ...problem });
    const finish = finishers[op.id];
    if (op.status === "running" && finish) {
      op.result = finish();
      op.status = "done";
      op.finished_at = new Date().toISOString();
    }
    return HttpResponse.json(op);
  }),
  http.get("*/api/v1/lab/state", () => HttpResponse.json(labStateFixture)),
  http.get("*/api/v1/workspaces/:workspace/tree", ({ params }) =>
    HttpResponse.json({
      workspace: String(params.workspace),
      files: [{ path: "hello.yaml", size: 40 }],
      bytes: 40,
      limit_bytes: 10485760,
    }),
  ),
  http.get("*/api/v1/workspaces/:workspace/files", () =>
    HttpResponse.json({ path: "hello.yaml", content: "apiVersion: v1\nkind: Pod\n", sha256: "0".repeat(64) }),
  ),
  http.get("*/api/v1/courses/:course/items/:item", ({ params }) => {
    const item = (fixtures.items as Record<string, Schemas["Item"]>)[String(params.item)];
    return params.course === fixtures.course.id && item
      ? HttpResponse.json(item)
      : HttpResponse.json(notFound(`item ${String(params.item)} is not in the catalogue`), { status: 404, ...problem });
  }),
);

export { http, HttpResponse, startQuizAttempt };
