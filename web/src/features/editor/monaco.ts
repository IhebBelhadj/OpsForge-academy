// Monaco (VS Code's editor) and monaco-vim in one lazily loaded chunk (frontend.md: heavy widgets load on the screens
// that use them). The editor core with all its features (find, multi-cursor, folding, F1's command palette...), and
// every language Monaco colours, each loaded when a file of it opens. No language services: no workers beyond the
// editor's own.
import "monaco-editor/features/register.all";
import "monaco-editor/languages/definitions/register.all";

import * as monaco from "monaco-editor/editor/editor.api";
import EditorWorker from "monaco-editor/editor/editor.worker?worker";
import { initVimMode, VimMode } from "monaco-vim";

self.MonacoEnvironment = { getWorker: () => new EditorWorker() };

/** The Vim emulation's API (CodeMirror's, which monaco-vim adapts): only what is used here. */
interface Register {
  setText(text: string, linewise?: boolean, blockwise?: boolean): void;
  pushText(text: string, linewise?: boolean): void;
  toString(): string;
  clear(): void;
}
interface RegisterController {
  registers: Record<string, Register>;
  pushText: (
    this: RegisterController,
    register: string | null | undefined,
    operator: string,
    text: string,
    linewise?: boolean,
    blockwise?: boolean,
  ) => void;
}
const vim = VimMode as unknown as {
  Vim: { getRegisterController(): RegisterController; defineRegister(name: string, register: Register): void };
  commands: Record<string, (cm: { editor: monaco.editor.IStandaloneCodeEditor }) => void>;
};

function copy(text: string, linewise?: boolean) {
  navigator.clipboard.writeText(linewise && !text.endsWith("\n") ? `${text}\n` : text).catch(() => undefined);
}

// Yanks go to the system clipboard, as `set clipboard=unnamedplus` does in Neovim: y, yy, Y and a visual y into the
// unnamed register (deletes stay in Vim's registers), and anything put in "+ or "*. Patched on the controller's
// prototype, so it holds when Vim resets its state.
const controller = vim.Vim.getRegisterController();
const proto = Object.getPrototypeOf(controller) as RegisterController;
const pushText = proto.pushText;
proto.pushText = function (this: RegisterController, register, operator, text, linewise, blockwise) {
  pushText.call(this, register, operator, text, linewise, blockwise);
  if (operator === "yank" && (!register || register === '"')) copy(text, linewise);
};
// "+ and "*: Vim's own registers (their prototype: monaco-vim keeps the Register class to itself) that also copy
const registerProto = Object.getPrototypeOf(controller.registers['"']) as Register;
for (const name of ["+", "*"]) {
  const r = Object.create(registerProto) as Register;
  r.clear();
  const set = registerProto.setText.bind(r);
  const push = registerProto.pushText.bind(r);
  r.setText = (text, linewise, blockwise) => {
    set(text, linewise, blockwise);
    copy(text, linewise);
  };
  r.pushText = (text, linewise) => {
    push(text, linewise);
    copy(r.toString());
  };
  vim.Vim.defineRegister(name, r);
}

// :w saves, as Ctrl+S does (the editor's "academy.save" action)
vim.commands.save = (cm) => {
  cm.editor.trigger("vim", "academy.save", null);
};

export { initVimMode, monaco };
