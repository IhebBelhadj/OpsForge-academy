import { act, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { renderRoute } from "@/test/render";
import { fixtures, http, HttpResponse, labAttempt, server, startQuizAttempt, tutorConversation } from "@/test/server";

vi.mock("@/features/lecture/highlight", () => ({ highlight: () => Promise.resolve(null) }));

// jsdom has no canvas for xterm: the terminal is a stand-in that connects at once and records what is typed
const typed: string[] = [];
vi.mock("@/features/terminal/terminal-pane", async () => {
  const { useEffect } = await import("react");
  return {
    TerminalPane: ({
      onStatus,
      onInput,
    }: {
      onStatus?: (s: string) => void;
      onInput?: (fn: ((t: string) => void) | null) => void;
    }) => {
      useEffect(() => {
        onStatus?.("connected");
        onInput?.((t: string) => typed.push(t));
        return () => onInput?.(null);
      }, []); // eslint-disable-line react-hooks/exhaustive-deps
      return <div role="application" aria-label="Terminal" />;
    },
  };
});

describe("the course player", () => {
  it("plays a lecture with its number, text and resources", async () => {
    const user = userEvent.setup();
    renderRoute("/learn/k8s-intro/what-is-a-pod");
    expect(await screen.findByRole("heading", { level: 1, name: "What is a pod" })).toBeInTheDocument();
    expect(screen.getByText("Lecture 1.1 · 8 min")).toBeInTheDocument();
    expect(screen.getByText(/share a network namespace/)).toBeInTheDocument();
    expect(screen.getByRole("complementary", { name: "Note" })).toBeInTheDocument();
    await user.click(screen.getByRole("tab", { name: "Resources (1)" }));
    expect(screen.getByRole("link", { name: "Kubernetes docs: Pods" })).toHaveAttribute("target", "_blank");
  });

  it("marks the current item in the course content and links the others", async () => {
    renderRoute("/learn/k8s-intro/pods-check");
    const sidebar = await screen.findByRole("navigation", { name: "Course content" });
    const current = await within(sidebar).findByRole("link", { current: "page" });
    expect(current).toHaveTextContent("1.2 Pods check");
    expect(within(sidebar).getByRole("link", { name: /1\.4 First pod/ })).toHaveAttribute(
      "href",
      "/learn/k8s-intro/first-pod",
    );
  });

  it("steps through the course with Previous and Next, and with the arrow keys", async () => {
    const user = userEvent.setup();
    const { router } = renderRoute("/learn/k8s-intro/what-is-a-pod");
    expect(await screen.findByRole("button", { name: "Previous" })).toBeDisabled();
    await user.click(screen.getByRole("link", { name: "Next: 1.2 Pods check" }));
    expect(await screen.findByRole("heading", { level: 1, name: "Pods check" })).toBeInTheDocument();
    await user.keyboard("{ArrowRight}");
    await waitFor(() => {
      expect(router.state.location.pathname).toBe("/learn/k8s-intro/meet-the-cluster");
    });
    await user.keyboard("{ArrowLeft}");
    await waitFor(() => {
      expect(router.state.location.pathname).toBe("/learn/k8s-intro/pods-check");
    });
  });

  it("takes a quiz on the server: intro, one question at a time, results, then a retake that passes", async () => {
    const user = userEvent.setup();
    renderRoute("/learn/k8s-intro/pods-check");
    expect(await screen.findByText("No attempts yet.")).toBeInTheDocument();
    expect(screen.getByText("70 %")).toBeInTheDocument();
    expect(screen.getByText("No limit")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Start quiz" }));
    expect(await screen.findByText("Question 1 of 2")).toBeInTheDocument();
    await user.click(screen.getByRole("radio", { name: "Nothing" }));
    expect(screen.getByRole("button", { name: "Question 1, answered" })).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: /Next/ }));
    expect(screen.getByText("Select all that apply")).toBeInTheDocument();
    for (const phase of ["Pending", "Running", "Succeeded"])
      await user.click(screen.getByRole("checkbox", { name: phase }));
    await user.click(screen.getByRole("button", { name: "Submit quiz" }));

    expect(await screen.findByRole("heading", { name: "Not passed yet: 1 of 2" })).toBeInTheDocument();
    expect(screen.getAllByLabelText("Wrong")).toHaveLength(1);
    expect(screen.getByText("A network namespace")).toBeInTheDocument(); // the key, revealed by the result
    expect(screen.getByRole("link", { name: "Review What is a pod" })).toHaveAttribute(
      "href",
      "/learn/k8s-intro/what-is-a-pod",
    );

    await user.click(screen.getByRole("button", { name: /Retake quiz/ }));
    await user.click(await screen.findByRole("radio", { name: "A network namespace" }));
    await user.click(screen.getByRole("button", { name: "Question 2, not answered" }));
    for (const phase of ["Pending", "Running", "Succeeded"])
      await user.click(screen.getByRole("checkbox", { name: phase }));
    await user.click(screen.getByRole("button", { name: "Submit quiz" }));
    expect(await screen.findByRole("heading", { name: "Passed: 2 of 2" })).toBeInTheDocument();
    expect(screen.getByText(/\+20 XP, your first pass/)).toBeInTheDocument();
  });

  it("continues an attempt left open, and submits it by itself when its time is up", async () => {
    const user = userEvent.setup();
    startQuizAttempt(-1); // its deadline has passed
    renderRoute("/learn/k8s-intro/pods-check");
    await user.click(await screen.findByRole("button", { name: "Continue attempt" }));
    expect(await screen.findByRole("heading", { name: "Not passed yet: 0 of 2" })).toBeInTheDocument();
    expect(screen.getAllByText("nothing (skipped)")).toHaveLength(2);
  });

  it("runs a lab: start, a hint, a failed check with what was wanted, the next step, then the pass", async () => {
    const user = userEvent.setup();
    renderRoute("/learn/k8s-intro/meet-the-cluster");
    expect(await screen.findByRole("heading", { name: /List the nodes/ })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Check" })).toBeDisabled();
    await user.click(screen.getByRole("button", { name: "Start lab" }));
    expect(await screen.findByText("Connected")).toBeInTheDocument();
    expect(screen.getByText("Step 1 of 2")).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: /Show hint 1 of 1/ }));
    expect(await screen.findByText(/Nodes are listed with/)).toBeInTheDocument();
    expect(screen.getByText(/1 hint used/)).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Check" }));
    const failed = await screen.findByRole("alert");
    expect(within(failed).getByText("== 3")).toBeInTheDocument();
    expect(within(failed).getByText("(none)")).toBeInTheDocument();
    await user.click(await screen.findByRole("button", { name: "Check" }));
    expect(await screen.findByText("Step 2 of 2")).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Applied" }));
    expect(await screen.findByRole("heading", { name: "Lab passed" })).toBeInTheDocument();
    expect(screen.getByText("+90")).toBeInTheDocument();
    expect(screen.getByText("7 min")).toBeInTheDocument();
  });

  it("serves the lab's text while OpsForge is out of reach, but cannot start it", async () => {
    server.use(http.get("*/readyz", () => HttpResponse.json(fixtures.labsOffline)));
    renderRoute("/learn/k8s-intro/meet-the-cluster");
    expect(await screen.findByRole("heading", { name: /List the nodes/ })).toBeInTheDocument();
    expect(await screen.findByText(/Labs are offline/)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Start lab" })).toBeDisabled();
  });

  it("types a step's command into the terminal with Run", async () => {
    const user = userEvent.setup();
    labAttempt(); // running already: the page picks it up
    renderRoute("/learn/k8s-intro/meet-the-cluster");
    await user.click(await screen.findByRole("button", { name: "Run in the terminal" }));
    expect(typed).toEqual(["opsforge kubectl get nodes\r"]);
  });

  it("refuses to start while another lab runs, and offers to abandon it", async () => {
    const user = userEvent.setup();
    labAttempt({ lab: "losing-a-site", course: null, item: null });
    renderRoute("/learn/k8s-intro/meet-the-cluster");
    expect(await screen.findByRole("heading", { name: "Another lab is running" })).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: /Abandon it/ }));
    expect(await screen.findByRole("button", { name: "Start lab" })).toBeInTheDocument();
  });

  it("abandons a running lab after asking, and resets one", async () => {
    const user = userEvent.setup();
    labAttempt({
      current_step: "apply",
      steps: [
        { id: "nodes", passed: true, hints: [] },
        { id: "apply", passed: false, hints: [] },
      ],
    });
    renderRoute("/learn/k8s-intro/meet-the-cluster");
    expect(await screen.findByText("Step 2 of 2")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: /Reset lab/ }));
    expect(await screen.findByText("Step 1 of 2")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: /Abandon/ }));
    await user.click(await screen.findByRole("button", { name: "Abandon lab" }));
    expect(await screen.findByRole("button", { name: "Start lab" })).toBeInTheDocument();
  });

  it("asks the tutor why a check failed, and talks with it", async () => {
    const user = userEvent.setup();
    labAttempt();
    renderRoute("/learn/k8s-intro/meet-the-cluster");
    await user.click(await screen.findByRole("button", { name: "Check" }));
    await user.click(await screen.findByRole("button", { name: /Ask the tutor why/ }));

    // No tutor yet: pick an agent; the question waits
    expect(await screen.findByRole("heading", { name: "Ask a tutor about this lab" })).toBeInTheDocument();
    expect(screen.getByText(/My check of the step "List the nodes" failed/)).toBeInTheDocument();
    expect(screen.getByRole("radio", { name: /Gemini CLI/ })).toBeDisabled();
    expect(screen.getByRole("radio", { name: /Claude Code/ })).toBeChecked();
    await user.click(screen.getByRole("button", { name: /Start the tutor/ }));

    // Once it is ready the question goes by itself, with what the check measured
    expect(await screen.findByText(/You asked: My check of the step "List the nodes" failed/)).toBeInTheDocument();
    const asked = tutorConversation()?.messages[0]?.text ?? "";
    expect(asked).toContain("- nodes.value: wanted == 3, the lab showed nothing");
    expect(asked).toContain("Don't give me the answer yet.");
    const looked = screen.getByRole("list", { name: "What the tutor looked at" });
    expect(within(looked).getByText("Read your steps")).toBeInTheDocument();
    expect(within(looked).getByText("Refused: rm -rf ~")).toBeInTheDocument();

    // A follow-up: Enter sends
    const box = screen.getByLabelText("Message the tutor");
    act(() => {
      box.focus();
    });
    await user.keyboard("and now?{Enter}");
    expect(await screen.findByText("You asked: and now?")).toBeInTheDocument();
    expect(box).toHaveValue("");

    await user.click(screen.getByRole("button", { name: /End/ }));
    expect(await screen.findByRole("heading", { name: "Ask a tutor about this lab" })).toBeInTheDocument();
    expect(tutorConversation()).toBeUndefined();
  });

  it("draws the lab map from the lab's state", async () => {
    renderRoute("/learn/k8s-intro/meet-the-cluster");
    // A click inside a resizable panel is a divider grab in jsdom (0×0 layout): focus activates the tab instead
    const tab = await screen.findByRole("tab", { name: /Lab map/ });
    act(() => {
      tab.focus();
    });
    const map = await screen.findByRole("img", { name: /Lab map/ });
    expect(map).toHaveAccessibleName(
      "Lab map: link a-b as configured, 40 ms; link a-c cut, 120 ms; link b-c changed live, 200 ms",
    );
    expect(screen.getByText("c-w1 (Stopped)")).toBeInTheDocument();
    expect(screen.queryByText("a-gw")).not.toBeInTheDocument();
  });

  it("solves a challenge: the starter fails, an edited manifest is accepted", async () => {
    const user = userEvent.setup();
    renderRoute("/learn/k8s-intro/first-pod");
    const editor = await screen.findByRole("textbox", { name: "Edit pod.yaml" });
    expect(editor).toHaveValue("apiVersion: v1\nkind: Pod\nmetadata:\n  name: web\n");
    expect(screen.getByText("Easy")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Submit" }));
    expect(await screen.findByText("Wrong answer", {}, { timeout: 2000 })).toBeInTheDocument();
    // jsdom lays everything out at 0×0, so the resizable panels take any click for a grab of their divider: focus the
    // editor directly
    editor.focus();
    await user.keyboard("spec: {{}}");
    await user.click(screen.getByRole("button", { name: "Submit" }));
    expect(await screen.findByText("Accepted", {}, { timeout: 2000 })).toBeInTheDocument();
    expect(screen.getByRole("tab", { name: "Solution" })).toBeInTheDocument();
  });

  it("says when the item is not in the course", async () => {
    renderRoute("/learn/k8s-intro/nope");
    expect(await screen.findByRole("heading", { name: "No such item" })).toBeInTheDocument();
  });
});
