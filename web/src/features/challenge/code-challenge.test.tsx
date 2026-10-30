import { act, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";

import type { components } from "@/api/schema";
import type { EditorSurfaceProps } from "@/features/editor";
import { renderRoute } from "@/test/render";
import { fixtures, http, HttpResponse, server } from "@/test/server";

type Schemas = components["schemas"];

vi.mock("@/features/lecture/highlight", () => ({ highlight: () => Promise.resolve(null) }));

// jsdom cannot run Monaco: the editor is a text area on the file shown
vi.mock("@/features/editor/editor-surface", () => ({
  EditorSurface: ({ path, text, onEdit }: EditorSurfaceProps) => (
    <textarea
      aria-label={`Edit ${path}`}
      value={text}
      onChange={(e) => {
        onEdit(path, e.target.value);
      }}
    />
  ),
}));

const starter = "def two_sum(nums, target):\n    pass\n";

/** first-pod made a coding challenge: Two Sum with one example. */
const twoSum: Schemas["Item"] = {
  ...fixtures.items["first-pod"],
  title: "Two Sum",
  challenge: {
    difficulty: "easy",
    xp: 50,
    needs: ["k3s"],
    statement: "Return the indices of the two numbers that add up to `target`.\n",
    files: ["solution.py", "solution.go"],
    starter: { "solution.py": starter, "solution.go": "package main\n" },
    time_limit_s: 10,
    tests: [],
    hints: 0,
    code: {
      function: "twoSum",
      params: ["nums", "target"],
      languages: ["python", "go"],
      compare: "unordered",
      examples: [{ args: [[2, 7, 11, 15], 9], want: [0, 1] }],
      cases: 3,
    },
  },
};

/** The platform judging Two Sum: code holding "wrong" answers [9, 9]; submissions are kept. */
function judge() {
  const submissions: Schemas["Submission"][] = [];
  const outcome = (code: string, submit: boolean): Schemas["ChallengeOutcome"] => {
    const wrong = code.includes("wrong");
    return {
      verdict: wrong ? "wrong_answer" : "accepted",
      passed: wrong ? (submit ? 1 : 0) : submit ? 3 : 1,
      total: submit ? 3 : 1,
      cases: wrong
        ? [{ n: submit ? 2 : 1, passed: false, args: [[3, 2, 4], 6], want: [1, 2], got: [9, 9], ms: 0.1 }]
        : submit
          ? []
          : [{ n: 1, passed: true, args: [[2, 7, 11, 15], 9], want: [0, 1], got: [0, 1], ms: 0.1 }],
      output: wrong ? "debugging" : "",
      seconds: 0.4,
      language: "python",
      xp: !wrong && submit && !submissions.some((s) => s.verdict === "accepted") ? 50 : 0,
      ...(submit ? { submission: submissions.length + 1 } : {}),
    };
  };
  const operation = (result: Schemas["ChallengeOutcome"], kind: "challenge.run" | "challenge.submit") => ({
    id: `op-${kind}`,
    kind,
    subject: "k8s-intro/first-pod",
    status: "done" as const,
    result,
    error: null,
    created_at: "2026-10-29T10:00:00Z",
    finished_at: "2026-10-29T10:00:01Z",
  });
  server.use(
    http.get("*/api/v1/courses/k8s-intro/items/first-pod", () => HttpResponse.json(twoSum)),
    http.post("*/api/v1/courses/k8s-intro/items/first-pod/runs", async ({ request }) => {
      const body = (await request.json()) as Schemas["CodeRequest"];
      return HttpResponse.json(operation(outcome(body.code, false), "challenge.run"), { status: 202 });
    }),
    http.post("*/api/v1/courses/k8s-intro/items/first-pod/submissions", async ({ request }) => {
      const body = (await request.json()) as Schemas["CodeRequest"];
      const result = outcome(body.code, true);
      submissions.unshift({
        id: submissions.length + 1,
        language: body.language,
        code: body.code,
        verdict: result.verdict,
        passed: result.passed,
        total: result.total,
        seconds: result.seconds,
        xp: result.xp,
        submitted_at: "2026-10-29T10:00:00Z",
        result,
      });
      return HttpResponse.json(operation(result, "challenge.submit"), { status: 202 });
    }),
    http.get("*/api/v1/courses/k8s-intro/items/first-pod/submissions", () => HttpResponse.json({ submissions })),
    http.get("*/api/v1/courses/k8s-intro/items/first-pod/solution", () =>
      submissions.some((s) => s.verdict === "accepted")
        ? HttpResponse.json({ files: { python: "def two_sum(nums, target):\n    return [0, 1]\n", go: "" } })
        : HttpResponse.json(
            { type: "/problems/solution-locked", title: "The solution is locked", status: 409 },
            { status: 409, headers: { "Content-Type": "application/problem+json" } },
          ),
    ),
  );
}

describe("a coding challenge", () => {
  beforeEach(() => {
    localStorage.clear();
  });

  it("shows the examples, runs them, then a wrong and a right submission", async () => {
    judge();
    const user = userEvent.setup();
    renderRoute("/learn/k8s-intro/first-pod");
    const editor = await screen.findByRole("textbox", { name: "Edit solution.py" });
    expect(editor).toHaveValue(starter);
    expect(screen.getByRole("heading", { level: 1, name: "Two Sum" })).toBeInTheDocument();
    expect(screen.getByText("two_sum(nums, target)")).toBeInTheDocument();
    expect(screen.getByText("3 test cases, 10 s limit")).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Run" }));
    expect(await screen.findByText("1 / 1 examples passed, 0.40 s")).toBeInTheDocument();
    expect(screen.getByText("Accepted")).toBeInTheDocument();

    // jsdom lays out at 0×0: the resizable panels take a click inside for a divider grab, so focus the editor
    editor.focus();
    await user.clear(editor);
    await user.keyboard("wrong");
    await user.click(screen.getByRole("button", { name: "Submit" }));
    expect(await screen.findByText("Wrong answer")).toBeInTheDocument();
    expect(screen.getByText("1 / 3 test cases passed, 0.40 s")).toBeInTheDocument();
    expect(screen.getByText("[9,9]")).toBeInTheDocument();
    expect(screen.getByText("debugging")).toBeInTheDocument();
    expect(screen.getByRole("tab", { name: "Submissions (1)" })).toBeInTheDocument();

    editor.focus();
    await user.clear(editor);
    await user.keyboard("right");
    await user.click(screen.getByRole("button", { name: "Submit" }));
    expect(await screen.findByText("+50 XP")).toBeInTheDocument();
    expect(await screen.findByRole("tab", { name: "Submissions (2)" })).toBeInTheDocument();
    // Tabs inside resizable panels activate on focus (a click would grab the divider)
    act(() => {
      screen.getByRole("tab", { name: "Submissions (2)" }).focus();
    });
    const table = screen.getByRole("table");
    expect(within(table).getAllByRole("row")).toHaveLength(3);
    expect(within(table).getByText("Wrong answer")).toBeInTheDocument();
  });

  it("keeps an unsent draft in the browser", async () => {
    judge();
    const user = userEvent.setup();
    const first = renderRoute("/learn/k8s-intro/first-pod");
    const editor = await screen.findByRole<HTMLTextAreaElement>("textbox", { name: "Edit solution.py" });
    editor.focus();
    editor.setSelectionRange(starter.length, starter.length);
    await user.keyboard("# draft");
    first.unmount();
    renderRoute("/learn/k8s-intro/first-pod");
    expect(await screen.findByRole("textbox", { name: "Edit solution.py" })).toHaveValue(`${starter}# draft`);
  });
});
