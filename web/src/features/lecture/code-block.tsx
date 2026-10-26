import { Check, Copy, Play } from "lucide-react";
import { useEffect, useState } from "react";

import { Button } from "@/components/ui/button";
import { useTerminalInput } from "@/features/terminal";

import { highlight } from "./highlight";

/** Blocks that are shell commands: Run types them into the lab's terminal. */
const shell = new Set(["", "bash", "sh", "shell", "console"]);

/** A fenced code block: plain at once, coloured when Shiki has loaded; Copy puts the code on the clipboard, Run
 * (in a lab with a connected terminal) types it into the shell and presses Enter. */
export function CodeBlock({ code, lang }: { code: string; lang: string }) {
  const [html, setHtml] = useState<string | null>(null);
  const [copied, setCopied] = useState(false);
  const type = useTerminalInput();
  const runnable = type && shell.has(lang);

  useEffect(() => {
    let live = true;
    if (lang)
      highlight(code, lang)
        .then((h) => {
          if (live) setHtml(h);
        })
        .catch(() => undefined); // stays plain
    return () => {
      live = false;
    };
  }, [code, lang]);

  async function copy() {
    await navigator.clipboard.writeText(code);
    setCopied(true);
    setTimeout(() => {
      setCopied(false);
    }, 1500);
  }

  return (
    <div className="my-5 overflow-hidden rounded-md border border-rule bg-enamel-deep/40 font-mono text-[0.85rem]">
      {/* The buttons sit in their own bar, so a long command never scrolls under them */}
      <div className="flex items-center justify-end gap-1.5 border-b border-rule/60 px-2 py-1">
        {lang ? <span className="mr-auto pl-1 font-sans text-xs text-ink-soft">{lang}</span> : null}
        {runnable ? (
          <Button
            size="sm"
            className="h-6 gap-1 px-2 text-xs"
            onClick={() => {
              type(`${code}\r`);
            }}
            aria-label="Run in the terminal"
          >
            <Play aria-hidden className="size-3.5" />
            Run
          </Button>
        ) : null}
        <Button
          variant="ghost"
          size="sm"
          className="h-6 gap-1 px-2 text-xs"
          onClick={() => void copy()}
          aria-label={copied ? "Copied" : "Copy code"}
        >
          {copied ? <Check aria-hidden className="size-3.5" /> : <Copy aria-hidden className="size-3.5" />}
          {copied ? "Copied" : "Copy"}
        </Button>
      </div>
      {html ? (
        // Shiki's output: the code escaped into spans, nothing from the lecture as markup
        <div className="code-shiki overflow-x-auto p-4" dangerouslySetInnerHTML={{ __html: html }} />
      ) : (
        <pre className="overflow-x-auto p-4">
          <code>{code}</code>
        </pre>
      )}
    </div>
  );
}
