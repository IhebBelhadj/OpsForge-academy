import { actions, ensureSession, type Operation } from "./client";

/** The page's one live stream, GET /api/v1/events (server-sent events): `operation` events carry a long operation's
 * new status, `history` events the learner's history as it is written. It opens with the first listener and closes
 * with the last; the browser reconnects by itself (Last-Event-ID). */
type Listener = (data: unknown) => void;
const listeners = new Map<string, Set<Listener>>();
let source: EventSource | undefined;

function open() {
  if (source || typeof EventSource === "undefined" || [...listeners.values()].every((s) => s.size === 0)) return;
  source = new EventSource(`${location.origin}/api/v1/events`);
  for (const name of ["operation", "history"])
    source.addEventListener(name, (e: MessageEvent<string>) => {
      let data: unknown;
      try {
        data = JSON.parse(e.data);
      } catch {
        return;
      }
      for (const l of listeners.get(name) ?? []) l(data);
    });
}

export function subscribe(event: "operation" | "history", listener: Listener): () => void {
  let set = listeners.get(event);
  if (!set) listeners.set(event, (set = new Set()));
  set.add(listener);
  // The stream needs the session cookie: open it once the page is signed in
  ensureSession().then(open, () => undefined);
  return () => {
    set.delete(listener);
    if ([...listeners.values()].every((s) => s.size === 0)) {
      source?.close();
      source = undefined;
    }
  };
}

/** Delays between asking for an operation directly, in case the stream is down or missed it. */
const polls = [250, 1000, 2000, 3000];

/** The operation once it is no longer running: from its `operation` event, or from GET /operations/{id}. */
export function awaitOperation(op: Operation): Promise<Operation> {
  if (op.status !== "running") return Promise.resolve(op);
  return new Promise((resolve) => {
    let done = false;
    let timer: ReturnType<typeof setTimeout> | undefined;
    const finish = (o: Operation) => {
      if (done || o.id !== op.id || o.status === "running") return;
      done = true;
      stop();
      clearTimeout(timer);
      resolve(o);
    };
    const stop = subscribe("operation", (data) => {
      finish(data as Operation);
    });
    const poll = (n: number) => {
      timer = setTimeout(
        () => {
          actions.operation(op.id).then(finish, () => undefined);
          if (!done) poll(n + 1);
        },
        polls[Math.min(n, polls.length - 1)],
      );
    };
    poll(0);
  });
}
