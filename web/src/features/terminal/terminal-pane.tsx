import { RotateCcw } from "lucide-react";
import { useEffect, useRef, useState } from "react";

import { actions } from "@/api/client";
import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";

export type TerminalStatus = "starting" | "connected" | "reconnecting" | "replaced" | "exited" | "lost";

/** Close codes of /ws/terminal (internal/httpapi/terminal.go). */
const EXITED = 1000;
const REPLACED = 4000;
/** Reconnects to the same session before giving up: the server keeps it 60 s without a client. */
const retries = [500, 1000, 2000, 4000, 8000, 16000];

const message: Partial<Record<TerminalStatus, string>> = {
  replaced: "This terminal is open in another tab.",
  exited: "The shell exited.",
  lost: "The connection to the terminal was lost.",
};

/** A shell in a lab attempt's workspace (frontend.md "Terminal"): xterm.js on /ws/terminal/{session}, binary frames
 * out, `{t:"i",d}` and `{t:"r",cols,rows}` in. A dropped connection comes back to the same session (its last 64 KiB
 * replayed); after `exit`, or once the session is gone, a new shell is one click away. `onInput` gets the function
 * that types into the shell while it is connected (null otherwise). */
export function TerminalPane({
  workspace,
  onStatus,
  onInput,
  className,
}: {
  workspace: string;
  onStatus?: (s: TerminalStatus) => void;
  onInput?: (type: ((text: string) => void) | null) => void;
  className?: string;
}) {
  const el = useRef<HTMLDivElement>(null);
  const [status, setStatus] = useState<TerminalStatus>("starting");
  const [generation, setGeneration] = useState(0); // a new shell
  const report = useRef({ onStatus, onInput });
  useEffect(() => {
    report.current = { onStatus, onInput };
  });

  useEffect(() => {
    const host = el.current;
    if (!host) return;
    const life = { disposed: false }; // an object, so checks after an await are not narrowed away
    let ws: WebSocket | undefined;
    let timer: ReturnType<typeof setTimeout> | undefined;
    let cleanup: () => void = () => undefined;
    const set = (s: TerminalStatus) => {
      if (life.disposed) return;
      setStatus(s);
      report.current.onStatus?.(s);
      if (s !== "connected") report.current.onInput?.(null);
    };

    void (async () => {
      const { Terminal, FitAddon, WebLinksAddon } = await import("./xterm");
      if (life.disposed) return;
      const css = getComputedStyle(document.documentElement);
      const term = new Terminal({
        cursorBlink: true,
        fontFamily: css.getPropertyValue("--font-mono").trim() || "monospace",
        fontSize: 13,
        lineHeight: 1.25,
        scrollback: 5000,
        theme: {
          background: css.getPropertyValue("--term-bg").trim(),
          foreground: css.getPropertyValue("--term-fg").trim(),
          cursor: css.getPropertyValue("--term-fg").trim(),
        },
      });
      const fit = new FitAddon();
      term.loadAddon(fit);
      term.loadAddon(new WebLinksAddon());
      term.open(host);
      fit.fit();

      const send = (frame: object) => {
        if (ws?.readyState === WebSocket.OPEN) ws.send(JSON.stringify(frame));
      };
      const type = (text: string) => {
        send({ t: "i", d: text });
        term.focus();
      };
      let session: string | undefined;
      let attempt = 0;
      const connect = async () => {
        try {
          session ??= await actions.openTerminal({ workspace, cols: term.cols, rows: term.rows });
        } catch {
          set("lost");
          return;
        }
        if (life.disposed) return;
        const scheme = location.protocol === "https:" ? "wss" : "ws";
        const socket = new WebSocket(`${scheme}://${location.host}/ws/terminal/${session}`);
        ws = socket;
        socket.binaryType = "arraybuffer";
        socket.onopen = () => {
          attempt = 0;
          set("connected");
          report.current.onInput?.(type);
          send({ t: "r", cols: term.cols, rows: term.rows });
        };
        socket.onmessage = (e: MessageEvent<ArrayBuffer | string>) => {
          term.write(typeof e.data === "string" ? e.data : new Uint8Array(e.data));
        };
        socket.onclose = (e) => {
          if (life.disposed || ws !== socket) return;
          const wait = retries[attempt];
          if (e.code === EXITED) set("exited");
          else if (e.code === REPLACED) set("replaced");
          else if (wait === undefined) set("lost");
          else {
            attempt++;
            set("reconnecting");
            timer = setTimeout(() => void connect(), wait);
          }
        };
      };
      const input = term.onData((d) => {
        send({ t: "i", d });
      });
      const resize = term.onResize(({ cols, rows }) => {
        send({ t: "r", cols, rows });
      });
      const observer = new ResizeObserver(() => {
        fit.fit();
      });
      observer.observe(host);
      term.focus();
      void connect();
      cleanup = () => {
        observer.disconnect();
        input.dispose();
        resize.dispose();
        term.dispose();
      };
    })();

    return () => {
      life.disposed = true;
      clearTimeout(timer);
      report.current.onInput?.(null);
      const socket = ws;
      ws = undefined;
      socket?.close(1000);
      cleanup();
    };
  }, [workspace, generation]);

  const note = message[status];
  return (
    <div className={cn("flex min-h-0 flex-col bg-term-bg", className)}>
      {note ? (
        <div role="status" className="flex items-center gap-3 border-b border-rule/30 px-4 py-2 text-sm text-term-fg">
          {note}
          <Button
            size="sm"
            variant="outline"
            className="h-7 bg-transparent text-term-fg"
            onClick={() => {
              setGeneration((g) => g + 1);
            }}
          >
            <RotateCcw aria-hidden /> {status === "replaced" ? "Use it here" : "New shell"}
          </Button>
        </div>
      ) : null}
      <div ref={el} role="application" aria-label="Terminal" className="min-h-0 flex-1 p-2" />
    </div>
  );
}
