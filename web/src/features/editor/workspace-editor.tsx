import { useQuery, useQueryClient } from "@tanstack/react-query";
import { ChevronDown, ChevronRight, FilePlus, FileText, PanelLeft, Pencil, RefreshCw, Trash2, X } from "lucide-react";
import { type SubmitEvent, type ReactNode, useEffect, useMemo, useState } from "react";

import { actions, keys, ProblemError, queries, type WorkspaceFile } from "@/api/client";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogClose,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogTitle,
} from "@/components/ui/dialog";
import { Switch } from "@/components/ui/switch";
import { useIsMobile } from "@/hooks/use-mobile";
import { cn } from "@/lib/utils";

import { EditorSurface } from "./editor-surface";

const vimKey = "academy.editor.vim";

function storedVim() {
  try {
    return localStorage.getItem(vimKey) === "on";
  } catch {
    return false;
  }
}

function storeVim(on: boolean) {
  try {
    localStorage.setItem(vimKey, on ? "on" : "off");
  } catch {
    // a private window: the choice lasts this page
  }
}

/** A question the editor waits on: a file changed on the lab under an edit, a tab closed unsaved, a delete. */
interface Asking {
  kind: "conflict" | "close" | "delete";
  path: string;
}

/** The attempt's workspace in a code editor, VS Code's layout: the explorer, a tab per open file, the editor (Monaco,
 * with Vim's keys for who wants them; Vim's yanks go to the clipboard) and a status bar. Saves are against the
 * version read (base_sha256), so a save never overwrites what the terminal changed since: the learner chooses. */
export function WorkspaceEditor({ workspace, active }: { workspace: string; active: boolean }) {
  const client = useQueryClient();
  const tree = useQuery(queries.workspaceTree(workspace));
  const [open, setOpen] = useState<string[]>([]);
  const [current, setCurrent] = useState<string>();
  const [drafts, setDrafts] = useState<Record<string, string>>({});
  const [vim, setVim] = useState(storedVim);
  const [vimStatus, setVimStatus] = useState<HTMLElement | null>(null);
  // On a phone the explorer starts hidden: the file takes the width
  const [explorer, setExplorer] = useState(!useIsMobile());
  const [cursor, setCursor] = useState<{ line: number; column: number; language: string }>();
  const [saving, setSaving] = useState<string>();
  const [error, setError] = useState<string>();
  const [asking, setAsking] = useState<Asking>();
  const [naming, setNaming] = useState<{ from?: string; value: string }>();

  // The first file opens by itself
  const files = tree.data?.files;
  const shown = current ?? (open.length ? undefined : files?.[0]?.path);
  const tabs = shown && !open.includes(shown) ? [...open, shown] : open;
  const file = useQuery({ ...queries.workspaceFile(workspace, shown ?? ""), enabled: Boolean(shown) });

  const fileKey = (path: string) => queries.workspaceFile(workspace, path).queryKey;
  const base = (path: string) => client.getQueryData<WorkspaceFile>(fileKey(path));

  // The terminal may have changed the files: read them again when the editor shows
  useEffect(() => {
    if (active) void client.invalidateQueries({ queryKey: keys.workspace(workspace) });
  }, [active, client, workspace]);

  // Unsaved edits: the browser asks before the page goes
  const unsaved = Object.keys(drafts).length > 0;
  useEffect(() => {
    if (!unsaved) return;
    const warn = (e: BeforeUnloadEvent) => {
      e.preventDefault();
    };
    addEventListener("beforeunload", warn);
    return () => {
      removeEventListener("beforeunload", warn);
    };
  }, [unsaved]);

  function show(path: string) {
    setOpen((o) => (o.includes(path) ? o : [...(shown && !o.includes(shown) ? [...o, shown] : o), path]));
    setCurrent(path);
    setError(undefined);
  }

  function drop(path: string) {
    setDrafts(({ [path]: _, ...rest }) => rest);
    const left = tabs.filter((p) => p !== path);
    setOpen(left);
    if (shown === path) setCurrent(left[Math.max(0, tabs.indexOf(path) - 1)] ?? "");
  }

  function edited(path: string, text: string) {
    setDrafts((d) => {
      if (text === base(path)?.content) {
        if (!(path in d)) return d;
        const { [path]: _, ...rest } = d;
        return rest;
      }
      return d[path] === text ? d : { ...d, [path]: text };
    });
  }

  async function save(path: string, overwrite = false) {
    const content = drafts[path];
    if (content === undefined) return;
    setSaving(path);
    setError(undefined);
    try {
      const saved = await actions.saveFile({
        workspace,
        path,
        content,
        ...(overwrite ? {} : { base: base(path)?.sha256 ?? "" }),
      });
      client.setQueryData(fileKey(path), saved);
      setDrafts(({ [path]: kept, ...rest }) => (kept === content ? rest : { ...rest, [path]: kept ?? content }));
      void client.invalidateQueries({ queryKey: keys.workspace(workspace), exact: true });
    } catch (err) {
      if (err instanceof ProblemError && err.status === 409) setAsking({ kind: "conflict", path });
      else setError(`${path} was not saved: ${err instanceof Error ? err.message : String(err)}`);
    } finally {
      setSaving(undefined);
    }
  }

  async function name(e: SubmitEvent<HTMLFormElement>) {
    e.preventDefault();
    if (!naming) return;
    const to = naming.value.trim().replace(/^\/+/, "");
    const from = naming.from;
    if (!to || to === from) {
      setNaming(undefined);
      return;
    }
    setError(undefined);
    try {
      if (from) {
        await actions.moveFile({ workspace, from, to });
        const draft = drafts[from];
        setDrafts(({ [from]: _, ...rest }) => (draft === undefined ? rest : { ...rest, [to]: draft }));
        setOpen((o) => o.map((p) => (p === from ? to : p)));
        if (shown === from) setCurrent(to);
        client.removeQueries({ queryKey: fileKey(from) });
      } else {
        const made = await actions.saveFile({ workspace, path: to, content: "", base: "" });
        client.setQueryData(fileKey(to), made);
        show(to);
      }
      setNaming(undefined);
      await tree.refetch();
    } catch (err) {
      setError(
        err instanceof ProblemError && err.status === 409
          ? `${to} exists already.`
          : err instanceof Error
            ? err.message
            : String(err),
      );
    }
  }

  async function remove(path: string) {
    setAsking(undefined);
    try {
      await actions.deleteFile({ workspace, path });
      drop(path);
      client.removeQueries({ queryKey: fileKey(path) });
      await tree.refetch();
    } catch (err) {
      setError(`${path} was not deleted: ${err instanceof Error ? err.message : String(err)}`);
    }
  }

  async function takeTheirs(path: string) {
    setAsking(undefined);
    setDrafts(({ [path]: _, ...rest }) => rest);
    await client.invalidateQueries({ queryKey: fileKey(path) });
  }

  const text = shown ? (drafts[shown] ?? file.data?.content) : undefined;

  return (
    <div className="flex h-full min-h-0 flex-col bg-term-bg text-term-fg">
      <div className="flex min-h-0 flex-1">
        {explorer ? (
          <Explorer
            files={files?.map((f) => f.path) ?? []}
            shown={shown}
            drafts={drafts}
            naming={naming}
            onNaming={setNaming}
            onName={(e) => void name(e)}
            onOpen={show}
            onDelete={(path) => {
              setAsking({ kind: "delete", path });
            }}
            onRefresh={() => void client.invalidateQueries({ queryKey: keys.workspace(workspace) })}
          />
        ) : null}
        <div className="flex min-w-0 flex-1 flex-col">
          <div aria-label="Open files" className="flex h-9 shrink-0 items-stretch overflow-x-auto bg-code-side">
            <button
              type="button"
              aria-label={explorer ? "Hide the explorer" : "Show the explorer"}
              aria-pressed={explorer}
              onClick={() => {
                setExplorer((x) => !x);
              }}
              className="flex w-9 shrink-0 items-center justify-center border-r border-code-rule text-code-soft hover:text-term-fg"
            >
              <PanelLeft className="size-4" aria-hidden />
            </button>
            {tabs.map((p) => {
              const dirty = p in drafts;
              return (
                <div
                  key={p}
                  className={cn(
                    "group flex shrink-0 items-center border-r border-code-rule text-[13px]",
                    p === shown ? "border-t-2 border-t-ember bg-term-bg text-term-fg" : "text-code-soft",
                  )}
                >
                  <button
                    type="button"
                    aria-current={p === shown ? "true" : undefined}
                    onClick={() => {
                      show(p);
                    }}
                    className="flex h-full items-center gap-1.5 pl-3 pr-1"
                  >
                    <FileText className="size-3.5" aria-hidden />
                    {basename(p)}
                    {dirty ? <span className="sr-only"> (unsaved)</span> : null}
                  </button>
                  <button
                    type="button"
                    aria-label={`Close ${p}`}
                    onClick={() => {
                      if (dirty) setAsking({ kind: "close", path: p });
                      else drop(p);
                    }}
                    className="mr-1.5 flex size-5 items-center justify-center rounded hover:bg-code-hover"
                  >
                    {dirty ? <span aria-hidden className="size-2 rounded-full bg-term-fg group-hover:hidden" /> : null}
                    <X className={cn("size-3.5", dirty && "hidden group-hover:block")} aria-hidden />
                  </button>
                </div>
              );
            })}
          </div>
          {shown && text !== undefined ? (
            <EditorSurface
              path={shown}
              text={text}
              open={tabs}
              vim={vim}
              vimStatus={vimStatus}
              onEdit={edited}
              onSave={() => void save(shown)}
              onCursor={setCursor}
              className="flex-1"
            />
          ) : (
            <p className="p-5 text-sm text-code-soft">
              {file.isError
                ? `${shown ?? ""} cannot open here: ${file.error.message}`
                : tree.data && !files?.length && !shown
                  ? "No files yet: make one with New file, or in the terminal."
                  : shown
                    ? "Opening…"
                    : "Open a file from the explorer."}
            </p>
          )}
        </div>
      </div>
      {error ? (
        <p role="alert" className="border-t border-code-rule bg-red/20 px-3 py-1.5 text-sm">
          {error}
        </p>
      ) : null}
      <footer className="flex h-7 shrink-0 items-center gap-4 border-t border-code-rule bg-code-side px-3 font-mono text-xs text-code-soft">
        {/* Vim's mode and its : command line (monaco-vim draws here) */}
        <div ref={setVimStatus} className={cn("vim-status min-w-0 flex-1 truncate", !vim && "hidden")} />
        {!vim ? <span className="flex-1" /> : null}
        {shown ? (
          <span role="status">
            {saving === shown ? "Saving…" : shown in drafts ? "Unsaved" : file.data ? "Saved" : ""}
          </span>
        ) : null}
        {cursor ? (
          <>
            <span>
              Ln {cursor.line}, Col {cursor.column}
            </span>
            <span>{cursor.language}</span>
          </>
        ) : null}
        <label htmlFor="editor-vim" className="flex items-center gap-2 font-sans">
          Vim
          <Switch
            id="editor-vim"
            size="sm"
            className="data-[state=checked]:bg-ember data-[state=unchecked]:bg-code-rule"
            checked={vim}
            onCheckedChange={(on) => {
              setVim(on);
              storeVim(on);
            }}
          />
        </label>
      </footer>

      <Dialog
        open={Boolean(asking)}
        onOpenChange={(o) => {
          if (!o) setAsking(undefined);
        }}
      >
        {asking ? (
          <DialogContent>
            {asking.kind === "conflict" ? (
              <>
                <DialogTitle>{asking.path} changed on the lab</DialogTitle>
                <DialogDescription>
                  It changed since you opened it, in the terminal or by the lab. Keep your version over it, or take the
                  lab's and lose your edits.
                </DialogDescription>
                <DialogFooter>
                  <Button variant="outline" onClick={() => void takeTheirs(asking.path)}>
                    Take the lab's version
                  </Button>
                  <Button
                    onClick={() => {
                      setAsking(undefined);
                      void save(asking.path, true);
                    }}
                  >
                    Keep mine
                  </Button>
                </DialogFooter>
              </>
            ) : asking.kind === "close" ? (
              <>
                <DialogTitle>Save the changes to {asking.path}?</DialogTitle>
                <DialogDescription>Your edits are lost if you close it without saving.</DialogDescription>
                <DialogFooter>
                  <DialogClose asChild>
                    <Button variant="ghost">Cancel</Button>
                  </DialogClose>
                  <Button
                    variant="outline"
                    onClick={() => {
                      setAsking(undefined);
                      drop(asking.path);
                    }}
                  >
                    Don't save
                  </Button>
                  <Button
                    onClick={() => {
                      setAsking(undefined);
                      void save(asking.path).then(() => {
                        drop(asking.path);
                      });
                    }}
                  >
                    Save
                  </Button>
                </DialogFooter>
              </>
            ) : (
              <>
                <DialogTitle>Delete {asking.path}?</DialogTitle>
                <DialogDescription>
                  It is deleted from the lab's workspace. Resetting the lab brings the lab's own files back.
                </DialogDescription>
                <DialogFooter>
                  <DialogClose asChild>
                    <Button variant="ghost">Cancel</Button>
                  </DialogClose>
                  <Button variant="destructive" onClick={() => void remove(asking.path)}>
                    Delete
                  </Button>
                </DialogFooter>
              </>
            )}
          </DialogContent>
        ) : null}
      </Dialog>
    </div>
  );
}

function basename(path: string) {
  return path.slice(path.lastIndexOf("/") + 1);
}

interface Folder {
  folders: Map<string, Folder>;
  files: string[];
}

/** Paths as folders, folders first, each sorted. */
function folderTree(paths: string[]): Folder {
  const root: Folder = { folders: new Map(), files: [] };
  for (const p of [...paths].sort()) {
    const parts = p.split("/");
    let at = root;
    for (const part of parts.slice(0, -1)) {
      let next = at.folders.get(part);
      if (!next) at.folders.set(part, (next = { folders: new Map(), files: [] }));
      at = next;
    }
    at.files.push(p);
  }
  return root;
}

function Explorer({
  files,
  shown,
  drafts,
  naming,
  onNaming,
  onName,
  onOpen,
  onDelete,
  onRefresh,
}: {
  files: string[];
  shown: string | undefined;
  drafts: Record<string, string>;
  naming: { from?: string; value: string } | undefined;
  onNaming: (n: { from?: string; value: string } | undefined) => void;
  onName: (e: SubmitEvent<HTMLFormElement>) => void;
  onOpen: (path: string) => void;
  onDelete: (path: string) => void;
  onRefresh: () => void;
}) {
  const root = useMemo(() => folderTree(files), [files]);
  const [closed, setClosed] = useState<Set<string>>(new Set());

  const nameInput = (
    <form onSubmit={onName} className="px-2 py-0.5">
      <input
        // eslint-disable-next-line jsx-a11y/no-autofocus -- it appears on the learner's own click, to be typed in
        autoFocus
        aria-label={naming?.from ? `New name for ${naming.from}` : "New file name"}
        value={naming?.value ?? ""}
        onChange={(e) => {
          onNaming({ ...naming, value: e.target.value });
        }}
        onKeyDown={(e) => {
          if (e.key === "Escape") onNaming(undefined);
        }}
        onBlur={() => {
          onNaming(undefined);
        }}
        placeholder="k8s/web.yaml"
        className="w-full rounded-sm border border-focus bg-term-bg px-1.5 py-0.5 font-mono text-[13px] text-term-fg outline-none"
      />
    </form>
  );

  function folder(f: Folder, prefix: string, depth: number): ReactNode {
    return (
      <>
        {[...f.folders.entries()].map(([part, sub]) => {
          const path = prefix + part;
          const isClosed = closed.has(path);
          return (
            <li key={path}>
              <button
                type="button"
                aria-expanded={!isClosed}
                onClick={() => {
                  setClosed((c) => {
                    const next = new Set(c);
                    if (isClosed) next.delete(path);
                    else next.add(path);
                    return next;
                  });
                }}
                style={{ paddingLeft: 8 + depth * 12 }}
                className="flex w-full items-center gap-1 py-0.5 text-left text-[13px] hover:bg-code-hover"
              >
                {isClosed ? (
                  <ChevronRight className="size-3.5" aria-hidden />
                ) : (
                  <ChevronDown className="size-3.5" aria-hidden />
                )}
                {part}
              </button>
              {isClosed ? null : <ul>{folder(sub, `${path}/`, depth + 1)}</ul>}
            </li>
          );
        })}
        {f.files.map((p) =>
          naming?.from === p ? (
            <li key={p}>{nameInput}</li>
          ) : (
            <li key={p} className="group relative">
              <button
                type="button"
                aria-current={p === shown ? "true" : undefined}
                onClick={() => {
                  onOpen(p);
                }}
                onKeyDown={(e) => {
                  if (e.key === "F2") onNaming({ from: p, value: p });
                  if (e.key === "Delete") onDelete(p);
                }}
                style={{ paddingLeft: 22 + depth * 12 }}
                className={cn(
                  "flex w-full items-center gap-1.5 py-0.5 pr-14 text-left font-mono text-[13px] hover:bg-code-hover",
                  p === shown && "bg-code-hover text-term-fg",
                )}
              >
                <FileText className="size-3.5 shrink-0 text-code-soft" aria-hidden />
                <span className="truncate">{basename(p)}</span>
                {p in drafts ? (
                  <span aria-label="unsaved" className="ml-auto size-2 shrink-0 rounded-full bg-term-fg" />
                ) : null}
              </button>
              <span className="absolute top-0 right-1 hidden gap-0.5 group-focus-within:flex group-hover:flex">
                <button
                  type="button"
                  aria-label={`Rename ${p}`}
                  onClick={() => {
                    onNaming({ from: p, value: p });
                  }}
                  className="rounded p-1 text-code-soft hover:bg-code-rule hover:text-term-fg"
                >
                  <Pencil className="size-3" aria-hidden />
                </button>
                <button
                  type="button"
                  aria-label={`Delete ${p}`}
                  onClick={() => {
                    onDelete(p);
                  }}
                  className="rounded p-1 text-code-soft hover:bg-code-rule hover:text-term-fg"
                >
                  <Trash2 className="size-3" aria-hidden />
                </button>
              </span>
            </li>
          ),
        )}
      </>
    );
  }

  return (
    <nav aria-label="Explorer" className="flex w-52 shrink-0 flex-col border-r border-code-rule bg-code-side">
      <div className="flex h-9 shrink-0 items-center justify-between pl-3 pr-1 text-[11px] font-semibold tracking-wider text-code-soft uppercase">
        Workspace
        <span className="flex">
          <button
            type="button"
            aria-label="New file"
            onClick={() => {
              onNaming({ value: "" });
            }}
            className="rounded p-1.5 hover:bg-code-hover hover:text-term-fg"
          >
            <FilePlus className="size-3.5" aria-hidden />
          </button>
          <button
            type="button"
            aria-label="Read the files again"
            onClick={onRefresh}
            className="rounded p-1.5 hover:bg-code-hover hover:text-term-fg"
          >
            <RefreshCw className="size-3.5" aria-hidden />
          </button>
        </span>
      </div>
      <ul className="min-h-0 flex-1 overflow-y-auto pb-2">
        {naming && naming.from === undefined ? <li>{nameInput}</li> : null}
        {folder(root, "", 0)}
      </ul>
    </nav>
  );
}
