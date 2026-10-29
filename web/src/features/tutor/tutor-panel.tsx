import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ArrowUp, Ban, Bot, Check, Loader2, Square, X } from "lucide-react";
import { type KeyboardEvent, type SyntheticEvent, useEffect, useRef, useState } from "react";

import {
  actions,
  keys,
  type LabAttempt,
  queries,
  type TutorAgent,
  type TutorConversation,
  type TutorEvent,
  type TutorMessage,
} from "@/api/client";
import { subscribe } from "@/api/events";
import { Lamp } from "@/components/lamp";
import { Button } from "@/components/ui/button";
import { Markdown } from "@/features/lecture";
import { cn } from "@/lib/utils";

import { busy, ended, rememberAgent, rememberedAgent, suggestions, toolLabel } from "./tutor";

/** A question for the tutor from elsewhere on the page ("Ask the tutor" on a failed check); id tells two apart. */
export interface TutorQuestion {
  id: number;
  text: string;
}

/** The lab's tutor (the Tutor tab): a conversation with a coding agent the learner has on this machine, about this
 * attempt. Its answers stream as `tutor` events, each one a cue to read the conversation again (the server's copy
 * is the truth); while the agent works, the page also reads it every second, in case the stream is down.
 * `question` is waiting to be asked (the page keeps it): it is sent once the tutor is ready, then `onAsked`. */
export function TutorPanel({
  attempt,
  question,
  onAsked,
}: {
  attempt: LabAttempt;
  question: TutorQuestion | undefined;
  onAsked: () => void;
}) {
  const client = useQueryClient();
  const agents = useQuery(queries.tutorAgents());
  const list = useQuery(queries.tutorConversations());
  const [opened, setOpened] = useState<string>();
  const id = opened ?? list.data?.find((c) => c.attempt === attempt.id)?.id;
  const conv = useQuery({
    ...queries.tutorConversation(id ?? ""),
    enabled: Boolean(id),
    refetchInterval: (q) => (busy(q.state.data) ? 1000 : false),
  });
  const c = id ? conv.data : undefined;
  const [error, setError] = useState<string>();

  useEffect(
    () =>
      subscribe("tutor", (data) => {
        const e = data as TutorEvent;
        void client.invalidateQueries({ queryKey: keys.tutorConversation(e.conversation) });
        if (e.type === "status") void client.invalidateQueries({ queryKey: keys.tutorConversations });
      }),
    [client],
  );

  const start = useMutation({
    mutationFn: actions.startTutor,
    onSuccess: (started) => {
      rememberAgent(started.agent);
      client.setQueryData(keys.tutorConversation(started.id), started);
      setOpened(started.id);
      setError(undefined);
      void client.invalidateQueries({ queryKey: keys.tutorConversations });
    },
    onError: (e) => {
      setError(e.message);
    },
  });
  const ask = useMutation({
    mutationFn: actions.askTutor,
    onSuccess: (answering) => {
      client.setQueryData(keys.tutorConversation(answering.id), answering);
      setError(undefined);
    },
    onError: (e) => {
      setError(e.message);
    },
  });
  const close = useMutation({
    mutationFn: actions.closeTutor,
    onSettled: () => {
      setOpened(undefined);
      client.removeQueries({ queryKey: keys.tutorConversation(id ?? "") });
      void client.invalidateQueries({ queryKey: keys.tutorConversations });
    },
  });

  // A question from the page without a tutor: start the agent the learner used last, when it is installed
  const last = agents.data?.find((a) => a.id === rememberedAgent() && a.available)?.id;
  const idle = !c || ended(c);
  const { mutate: startAgent, isPending: starting } = start;
  useEffect(() => {
    if (question && idle && last && !starting) startAgent({ agent: last, attempt: attempt.id });
    // A new question only, not every render while it waits
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [question?.id, last]);
  // ... and ask it once the tutor is ready
  const readyId = c?.status === "ready" ? c.id : undefined;
  const { mutate: send, isPending: sending } = ask;
  useEffect(() => {
    if (!question || !readyId || sending) return;
    send({ conversation: readyId, text: question.text });
    onAsked();
  }, [question, readyId, sending, send, onAsked]);

  if (!c || conv.isError) {
    return (
      <AgentPicker
        agents={agents.data}
        loading={agents.isPending}
        starting={start.isPending}
        pending={question?.text}
        error={error}
        onStart={(agent) => {
          start.mutate({ agent, attempt: attempt.id });
        }}
      />
    );
  }

  const agentName = agents.data?.find((a) => a.id === c.agent)?.name ?? c.agent;
  return (
    <div className="flex h-full flex-col bg-surface">
      <div className="flex items-center gap-3 border-b border-rule px-4 py-2 text-sm">
        <Bot aria-hidden className="size-4 text-ink-soft" />
        <span className="font-semibold">{agentName}</span>
        <span className="flex items-center gap-1.5 text-ink-soft" role="status">
          <Lamp on={statusLamp[c.status]} />
          {statusName[c.status]}
        </span>
        <Button
          variant="ghost"
          size="sm"
          className="ml-auto"
          disabled={close.isPending}
          onClick={() => {
            close.mutate(c.id);
          }}
        >
          <X aria-hidden /> {ended(c) ? "Dismiss" : "End"}
        </Button>
      </div>
      <Transcript conversation={c} agentName={agentName} />
      {ended(c) ? (
        <div className="border-t border-rule p-4 text-sm">
          <p className="text-ink-soft">{c.error ?? "This conversation has ended."}</p>
          <Button
            className="mt-3"
            size="sm"
            disabled={start.isPending}
            onClick={() => {
              start.mutate({ agent: c.agent, attempt: attempt.id });
            }}
          >
            Start again with {agentName}
          </Button>
        </div>
      ) : (
        <Composer
          conversation={c}
          error={error}
          sending={ask.isPending}
          onSend={(text) => {
            ask.mutate({ conversation: c.id, text });
          }}
          onStop={() => {
            void actions.cancelTutor(c.id);
          }}
        />
      )}
    </div>
  );
}

const statusName: Record<TutorConversation["status"], string> = {
  starting: "Starting",
  ready: "Ready",
  answering: "Answering",
  failed: "Stopped",
  closed: "Ended",
};
const statusLamp = {
  starting: "amber",
  ready: "green",
  answering: "amber",
  failed: "red",
  closed: "off",
} as const;

function AgentPicker({
  agents,
  loading,
  starting,
  pending,
  error,
  onStart,
}: {
  agents: TutorAgent[] | undefined;
  loading: boolean;
  starting: boolean;
  pending: string | undefined;
  error: string | undefined;
  onStart: (agent: string) => void;
}) {
  const remembered = rememberedAgent();
  const firstAvailable = agents?.find((a) => a.id === remembered && a.available) ?? agents?.find((a) => a.available);
  const [choice, setChoice] = useState<string>();
  const picked = choice ?? firstAvailable?.id;
  return (
    <div className="h-full overflow-auto bg-surface p-5 sm:p-6">
      <div className="max-w-lg">
        <h2 className="text-xl font-semibold">Ask a tutor about this lab</h2>
        <p className="mt-2 text-sm text-ink-soft">
          Academy starts a coding agent you already use, on this machine and signed in as you. It sees what you see,
          your steps, your terminal and the lab, through read-only tools. It explains and points you at the evidence; it
          cannot run commands or change your files.
        </p>
        {pending ? (
          <div className="mt-4 rounded-md border border-rule bg-enamel/60 p-3 text-sm">
            <p className="font-semibold">Your question, sent once the tutor is ready</p>
            <p className="mt-1 line-clamp-3 whitespace-pre-line text-ink-soft">{pending}</p>
          </div>
        ) : null}
        <fieldset className="mt-5">
          <legend className="font-semibold">Agent</legend>
          {loading ? <p className="mt-2 text-sm text-ink-soft">Looking for agents…</p> : null}
          <div className="mt-2 grid gap-2">
            {agents?.map((a) => (
              <label
                key={a.id}
                className={cn(
                  "flex cursor-pointer items-start gap-3 rounded-lg border border-rule p-3",
                  picked === a.id && a.available && "border-ink",
                  !a.available && "cursor-not-allowed opacity-60",
                )}
              >
                <input
                  type="radio"
                  name="tutor-agent"
                  value={a.id}
                  className="mt-1 accent-ink"
                  checked={picked === a.id}
                  disabled={!a.available}
                  onChange={() => {
                    setChoice(a.id);
                  }}
                />
                <span>
                  <span className="font-semibold">{a.name}</span>
                  {a.detail ? <span className="block text-sm text-ink-soft">{a.detail}</span> : null}
                </span>
              </label>
            ))}
          </div>
        </fieldset>
        {error ? (
          <p role="alert" className="mt-4 rounded-md border border-red/40 bg-red-soft/40 p-3 text-sm">
            {error}
          </p>
        ) : null}
        <Button
          className="mt-5"
          disabled={!picked || starting}
          onClick={() => {
            if (picked) onStart(picked);
          }}
        >
          {starting ? <Loader2 aria-hidden className="animate-spin motion-reduce:animate-none" /> : <Bot aria-hidden />}
          Start the tutor
        </Button>
      </div>
    </div>
  );
}

function Transcript({ conversation: c, agentName }: { conversation: TutorConversation; agentName: string }) {
  const end = useRef<HTMLDivElement>(null);
  const last = c.messages.at(-1);
  useEffect(() => {
    end.current?.scrollIntoView({ block: "end" });
  }, [c.messages.length, last?.text.length, last?.tools.length]);
  return (
    <div className="min-h-0 flex-1 overflow-auto px-4 py-4" aria-live="polite" aria-busy={busy(c)}>
      {c.status === "starting" ? (
        <p className="flex items-center gap-2 text-sm text-ink-soft">
          <Loader2 aria-hidden className="size-4 animate-spin motion-reduce:animate-none" />
          Starting {agentName}. The first start can take a minute while its adapter is fetched.
        </p>
      ) : null}
      {c.status === "ready" && c.messages.length === 0 ? (
        <p className="text-sm text-ink-soft">{agentName} is ready. Ask about a step, an error, or what you see.</p>
      ) : null}
      <ol className="space-y-4">
        {c.messages.map((m, i) => (
          <MessageRow
            key={i} // messages are only ever appended
            message={m}
            streaming={c.status === "answering" && i === c.messages.length - 1}
          />
        ))}
      </ol>
      <div ref={end} />
    </div>
  );
}

function MessageRow({ message: m, streaming }: { message: TutorMessage; streaming: boolean }) {
  if (m.role === "learner") {
    return (
      <li className="ml-8 rounded-lg bg-enamel px-3 py-2 text-sm whitespace-pre-wrap">
        <span className="sr-only">You: </span>
        {m.text}
      </li>
    );
  }
  return (
    <li className="mr-4">
      <span className="sr-only">Tutor: </span>
      {m.tools.length ? (
        <ul aria-label="What the tutor looked at" className="mb-2 flex flex-wrap gap-1.5">
          {m.tools.map((t) => (
            <li
              key={t.id}
              className={cn(
                "flex items-center gap-1 rounded-full border border-rule px-2 py-0.5 text-xs text-ink-soft",
                t.status === "denied" && "border-red/40 text-red",
              )}
            >
              {t.status === "denied" ? (
                <Ban aria-hidden className="size-3" />
              ) : t.status === "completed" ? (
                <Check aria-hidden className="size-3" />
              ) : t.status === "failed" ? (
                <X aria-hidden className="size-3" />
              ) : (
                <Loader2 aria-hidden className="size-3 animate-spin motion-reduce:animate-none" />
              )}
              {toolLabel(t)}
            </li>
          ))}
        </ul>
      ) : null}
      {m.text ? (
        <Markdown variant="compact" className="text-sm">
          {m.text}
        </Markdown>
      ) : streaming ? (
        <p className="flex items-center gap-2 text-sm text-ink-soft">
          <Loader2 aria-hidden className="size-4 animate-spin motion-reduce:animate-none" /> Thinking…
        </p>
      ) : null}
    </li>
  );
}

function Composer({
  conversation: c,
  error,
  sending,
  onSend,
  onStop,
}: {
  conversation: TutorConversation;
  error: string | undefined;
  sending: boolean;
  onSend: (text: string) => void;
  onStop: () => void;
}) {
  const [draft, setDraft] = useState("");
  const ready = c.status === "ready" && !sending;
  function submit(e?: SyntheticEvent) {
    e?.preventDefault();
    const text = draft.trim();
    if (!text || !ready) return;
    onSend(text);
    setDraft("");
  }
  function keyDown(e: KeyboardEvent<HTMLTextAreaElement>) {
    if (e.key === "Enter" && !e.shiftKey && !e.nativeEvent.isComposing) submit(e);
  }
  return (
    <div className="border-t border-rule p-3">
      {c.messages.length === 0 && ready ? (
        <div className="mb-2 flex flex-wrap gap-1.5">
          {suggestions.map((s) => (
            <Button
              key={s}
              variant="outline"
              size="sm"
              className="h-auto bg-surface py-1 text-xs font-normal"
              onClick={() => {
                onSend(s);
              }}
            >
              {s}
            </Button>
          ))}
        </div>
      ) : null}
      {error ? (
        <p role="alert" className="mb-2 text-sm text-red">
          {error}
        </p>
      ) : null}
      <form onSubmit={submit} className="flex items-end gap-2">
        <label htmlFor="tutor-message" className="sr-only">
          Message the tutor
        </label>
        <textarea
          id="tutor-message"
          rows={2}
          value={draft}
          placeholder={c.status === "starting" ? "The tutor is starting…" : "Ask about this lab (Enter to send)"}
          onChange={(e) => {
            setDraft(e.target.value);
          }}
          onKeyDown={keyDown}
          className="max-h-40 min-h-10 flex-1 resize-none rounded-md border border-control bg-surface px-3 py-2 text-sm outline-none focus-visible:border-ink"
        />
        {c.status === "answering" ? (
          <Button type="button" variant="outline" className="bg-surface" onClick={onStop}>
            <Square aria-hidden /> Stop
          </Button>
        ) : (
          <Button type="submit" disabled={!ready || !draft.trim()}>
            <ArrowUp aria-hidden /> Send
          </Button>
        )}
      </form>
    </div>
  );
}
