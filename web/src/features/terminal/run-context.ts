import { createContext, useContext } from "react";

/** Types text into the lab's terminal (a command block's Run and Paste); null while no terminal is connected. */
export const TerminalInput = createContext<((text: string) => void) | null>(null);

export function useTerminalInput() {
  return useContext(TerminalInput);
}
