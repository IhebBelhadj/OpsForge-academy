import { useEffect, useEffectEvent, useRef, useState } from "react";

import { cn } from "@/lib/utils";

type Chunk = typeof import("./monaco");
type Editor = ReturnType<Chunk["monaco"]["editor"]["create"]>;
type Model = NonNullable<ReturnType<Editor["getModel"]>>;
type ViewState = ReturnType<Editor["saveViewState"]>;

let chunk: Promise<Chunk> | undefined;

/** The Monaco chunk, loaded once; its theme follows the app's terminal colours. */
function loadMonaco() {
  chunk ??= import("./monaco").then((m) => {
    const css = getComputedStyle(document.documentElement);
    const v = (name: string) => css.getPropertyValue(name).trim();
    m.monaco.editor.defineTheme("academy", {
      base: "vs-dark",
      inherit: true,
      rules: [],
      colors: {
        "editor.background": v("--term-bg"),
        "editor.foreground": v("--term-fg"),
        "editorGutter.background": v("--term-bg"),
        "minimap.background": v("--term-bg"),
      },
    });
    return m;
  });
  chunk.catch(() => {
    chunk = undefined; // the next mount tries again
  });
  return chunk;
}

export interface EditorSurfaceProps {
  /** The file shown, and its text: the editor follows `text` when it changes from outside (a reload, a revert). */
  path: string;
  text: string;
  /** The open files: the editor keeps a model (and its undo history) for each, and drops the others. */
  open: string[];
  vim: boolean;
  /** Where Vim's mode and its : command line show. */
  vimStatus: HTMLElement | null;
  onEdit: (path: string, text: string) => void;
  /** Ctrl+S, or :w in Vim. */
  onSave: () => void;
  onCursor?: (at: { line: number; column: number; language: string }) => void;
  className?: string;
}

/** VS Code's editor (Monaco) on one workspace file at a time, with Vim's keys when `vim` is on. */
export function EditorSurface({
  path,
  text,
  open,
  vim,
  vimStatus,
  onEdit,
  onSave,
  onCursor,
  className,
}: EditorSurfaceProps) {
  const host = useRef<HTMLDivElement>(null);
  const [m, setM] = useState<Chunk>();
  const [failed, setFailed] = useState<string>();
  const [editor, setEditor] = useState<Editor>();
  const models = useRef(new Map<string, Model>());
  const views = useRef(new Map<string, ViewState>());

  const edit = useEffectEvent(onEdit);
  const save = useEffectEvent(onSave);
  const cursor = useEffectEvent(() => {
    const at = editor?.getPosition();
    const model = editor?.getModel();
    if (at && model) onCursor?.({ line: at.lineNumber, column: at.column, language: model.getLanguageId() });
  });

  useEffect(() => {
    let live = true;
    loadMonaco().then(
      (loaded) => {
        if (live) setM(loaded);
      },
      (err: unknown) => {
        if (live) setFailed(err instanceof Error ? err.message : String(err));
      },
    );
    return () => {
      live = false;
    };
  }, []);

  // One editor for the component's life; a model per open file
  useEffect(() => {
    if (!m || !host.current) return;
    const e = m.monaco.editor.create(host.current, {
      theme: "academy",
      automaticLayout: true,
      fontFamily: getComputedStyle(document.documentElement).getPropertyValue("--font-mono").trim(),
      fontSize: 14,
      lineHeight: 22,
      tabSize: 2,
      insertSpaces: true,
      minimap: { enabled: true },
      scrollBeyondLastLine: false,
      renderWhitespace: "selection",
      bracketPairColorization: { enabled: true },
      guides: { bracketPairs: true, indentation: true },
      stickyScroll: { enabled: true },
      smoothScrolling: true,
      cursorSmoothCaretAnimation: "on",
      fixedOverflowWidgets: true,
      padding: { top: 8 },
      model: null,
    });
    e.addAction({
      id: "academy.save",
      label: "File: Save",
      keybindings: [m.monaco.KeyMod.CtrlCmd | m.monaco.KeyCode.KeyS],
      run: () => {
        save();
      },
    });
    const subs = [
      e.onDidChangeModelContent(() => {
        const model = e.getModel();
        if (model) edit(pathOf(model), model.getValue());
      }),
      e.onDidChangeCursorPosition(() => {
        cursor();
      }),
      e.onDidChangeModel(() => {
        cursor();
      }),
    ];
    const kept = models.current;
    setEditor(e);
    return () => {
      for (const s of subs) s.dispose();
      e.dispose();
      for (const model of kept.values()) model.dispose();
      kept.clear();
      setEditor(undefined);
    };
  }, [m]);

  // Show the file: its model (made with its text the first time), and where its cursor was
  useEffect(() => {
    if (!m || !editor) return;
    let model = models.current.get(path);
    if (!model) {
      model = m.monaco.editor.createModel(text, undefined, m.monaco.Uri.file(path));
      models.current.set(path, model);
    }
    const before = editor.getModel();
    if (before === model) return;
    if (before) views.current.set(pathOf(before), editor.saveViewState());
    editor.setModel(model);
    const view = views.current.get(path);
    if (view) editor.restoreViewState(view);
    editor.focus();
    // `text` seeds a new model only; the next effect follows it afterwards
  }, [m, editor, path]); // eslint-disable-line react-hooks/exhaustive-deps

  // Follow `text` when it changes from outside, as an edit (it can be undone)
  useEffect(() => {
    const model = models.current.get(path);
    if (!model || model.getValue() === text) return;
    model.pushEditOperations([], [{ range: model.getFullModelRange(), text }], () => null);
  }, [editor, path, text]);

  // Drop the models of files no longer open
  useEffect(() => {
    for (const [p, model] of models.current)
      if (!open.includes(p) && p !== path) {
        model.dispose();
        models.current.delete(p);
        views.current.delete(p);
      }
  }, [open, path]);

  useEffect(() => {
    if (!m || !editor || !vim) return;
    const adapter = m.initVimMode(editor, vimStatus);
    return () => {
      adapter.dispose();
    };
  }, [m, editor, vim, vimStatus]);

  if (failed) return <p className="p-5 text-sm text-ink-soft">The editor did not load: {failed}</p>;
  return <div ref={host} className={cn("min-h-0", className)} data-testid="code-editor" />;
}

/** A model's workspace path ("/k8s/web.yaml" → "k8s/web.yaml"). */
function pathOf(model: Model) {
  return model.uri.path.replace(/^\//, "");
}
