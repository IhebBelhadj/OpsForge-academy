import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { renderWithQuery } from "@/test/render";
import { writeWorkspaceFile } from "@/test/server";

import type { EditorSurfaceProps } from "./editor-surface";
import { WorkspaceEditor } from "./workspace-editor";

// jsdom cannot run Monaco: the editor is a text area that edits the file shown, with Save standing in for Ctrl+S / :w
vi.mock("./editor-surface", () => ({
  EditorSurface: ({ path, text, vim, onEdit, onSave }: EditorSurfaceProps) => (
    <div>
      <textarea
        aria-label={`Edit ${path}`}
        data-vim={vim}
        value={text}
        onChange={(e) => {
          onEdit(path, e.target.value);
        }}
      />
      <button type="button" onClick={onSave}>
        Save (Ctrl+S)
      </button>
    </div>
  ),
}));

function renderEditor() {
  return renderWithQuery(<WorkspaceEditor workspace="ws-1" active />);
}

describe("the workspace editor", () => {
  it("opens the first file, edits and saves it against the version it read", async () => {
    const user = userEvent.setup();
    renderEditor();
    const editor = await screen.findByRole("textbox", { name: "Edit hello.yaml" });
    expect(editor).toHaveValue("apiVersion: v1\nkind: Pod\n");
    expect(screen.getByRole("status")).toHaveTextContent("Saved");

    await user.type(editor, "metadata: {{}}");
    expect(screen.getByRole("status")).toHaveTextContent("Unsaved");
    expect(screen.getByRole("button", { name: /^hello\.yaml ?\(unsaved\)$/ })).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Save (Ctrl+S)" }));
    await waitFor(() => {
      expect(screen.getByRole("status")).toHaveTextContent("Saved");
    });
    expect(within(screen.getByLabelText("Open files")).getByRole("button", { name: "hello.yaml" })).toBeInTheDocument();
  });

  it("opens files from the explorer, folders included, each in its tab", async () => {
    const user = userEvent.setup();
    renderEditor();
    await screen.findByRole("textbox", { name: "Edit hello.yaml" });
    const explorer = screen.getByRole("navigation", { name: "Explorer" });
    expect(within(explorer).getByRole("button", { name: "k8s" })).toHaveAttribute("aria-expanded", "true");
    await user.click(within(explorer).getByRole("button", { name: "web.yaml" }));
    expect(await screen.findByRole("textbox", { name: "Edit k8s/web.yaml" })).toHaveValue("kind: Deployment\n");
    const tabs = screen.getByLabelText("Open files");
    expect(within(tabs).getByRole("button", { name: "hello.yaml" })).toBeInTheDocument();
    expect(within(tabs).getByRole("button", { name: "web.yaml" })).toHaveAttribute("aria-current", "true");
  });

  it("asks before saving over a change made on the lab, and keeps the learner's version when told", async () => {
    const user = userEvent.setup();
    renderEditor();
    const editor = await screen.findByRole("textbox", { name: "Edit hello.yaml" });
    await user.type(editor, "# mine");
    writeWorkspaceFile("hello.yaml", "changed in the terminal\n");

    await user.click(screen.getByRole("button", { name: "Save (Ctrl+S)" }));
    const dialog = await screen.findByRole("dialog", { name: "hello.yaml changed on the lab" });
    await user.click(within(dialog).getByRole("button", { name: "Keep mine" }));
    await waitFor(() => {
      expect(screen.getByRole("status")).toHaveTextContent("Saved");
    });
    expect(editor).toHaveValue("apiVersion: v1\nkind: Pod\n# mine");
  });

  it("takes the lab's version over unsaved edits when told", async () => {
    const user = userEvent.setup();
    renderEditor();
    const editor = await screen.findByRole("textbox", { name: "Edit hello.yaml" });
    await user.type(editor, "# mine");
    writeWorkspaceFile("hello.yaml", "changed in the terminal\n");

    await user.click(screen.getByRole("button", { name: "Save (Ctrl+S)" }));
    const dialog = await screen.findByRole("dialog", { name: "hello.yaml changed on the lab" });
    await user.click(within(dialog).getByRole("button", { name: "Take the lab's version" }));
    await waitFor(() => {
      expect(editor).toHaveValue("changed in the terminal\n");
    });
    expect(screen.getByRole("status")).toHaveTextContent("Saved");
  });

  it("makes, renames and deletes files", async () => {
    const user = userEvent.setup();
    renderEditor();
    await screen.findByRole("textbox", { name: "Edit hello.yaml" });

    await user.click(screen.getByRole("button", { name: "New file" }));
    await user.type(screen.getByRole("textbox", { name: "New file name" }), "k8s/svc.yaml{Enter}");
    expect(await screen.findByRole("textbox", { name: "Edit k8s/svc.yaml" })).toHaveValue("");

    await user.click(screen.getByRole("button", { name: "Rename k8s/svc.yaml" }));
    const name = screen.getByRole("textbox", { name: "New name for k8s/svc.yaml" });
    await user.clear(name);
    await user.type(name, "k8s/service.yaml{Enter}");
    expect(await screen.findByRole("textbox", { name: "Edit k8s/service.yaml" })).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Delete k8s/service.yaml" }));
    const dialog = await screen.findByRole("dialog", { name: "Delete k8s/service.yaml?" });
    await user.click(within(dialog).getByRole("button", { name: "Delete" }));
    await waitFor(() => {
      expect(screen.queryByRole("button", { name: "service.yaml" })).not.toBeInTheDocument();
    });
    expect(await screen.findByRole("textbox", { name: "Edit hello.yaml" })).toBeInTheDocument();
  });

  it("asks before closing a tab with unsaved edits", async () => {
    const user = userEvent.setup();
    renderEditor();
    await user.type(await screen.findByRole("textbox", { name: "Edit hello.yaml" }), "x");
    await user.click(screen.getByRole("button", { name: "Close hello.yaml" }));
    const dialog = await screen.findByRole("dialog", { name: "Save the changes to hello.yaml?" });
    await user.click(within(dialog).getByRole("button", { name: "Don't save" }));
    expect(screen.queryByRole("textbox", { name: "Edit hello.yaml" })).not.toBeInTheDocument();
    expect(screen.getByText("Open a file from the explorer.")).toBeInTheDocument();
  });

  it("turns Vim's keys on, and remembers it", async () => {
    const user = userEvent.setup();
    const { unmount } = renderEditor();
    const editor = await screen.findByRole("textbox", { name: "Edit hello.yaml" });
    expect(editor).toHaveAttribute("data-vim", "false");
    await user.click(screen.getByRole("switch", { name: "Vim" }));
    expect(editor).toHaveAttribute("data-vim", "true");
    unmount();
    renderEditor();
    expect(await screen.findByRole("textbox", { name: "Edit hello.yaml" })).toHaveAttribute("data-vim", "true");
    localStorage.clear();
  });
});
