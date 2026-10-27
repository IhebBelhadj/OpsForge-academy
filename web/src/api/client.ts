import createClient, { type Middleware } from "openapi-fetch";

import type { components, paths } from "./schema";

/** Typed client for the platform API, generated from academy/api/openapi.yaml (`pnpm gen`). Same origin.
 * `fetch` is looked up per call, not captured at import, so test interceptors (MSW) see the requests. */
export const api = createClient<paths>({
  baseUrl: location.origin,
  fetch: (request) => globalThis.fetch(request),
});

type Schemas = components["schemas"];
export type Readiness = Schemas["Readiness"];
export type Problem = Schemas["Problem"];
export type Me = Schemas["Me"];
export type CourseSummary = Schemas["CourseSummary"];
export type Course = Schemas["Course"];
export type ItemRef = Schemas["ItemRef"];
export type Item = Schemas["Item"];
export type ItemType = Schemas["ItemType"];
export type QuizAttempt = Schemas["QuizAttempt"];
export type QuizQuestion = Schemas["QuizQuestion"];
export type QuizAttemptSummary = Schemas["QuizAttemptSummary"];
export type LabAttempt = Schemas["LabAttempt"];
export type LabOutcome = Schemas["LabOutcome"];
export type Operation = Schemas["Operation"];
export type LabState = Schemas["LabState"];
export type WorkspaceFile = Schemas["WorkspaceFile"];

/** A problem document the API answered, as an Error (queries reject with it). */
export class ProblemError extends Error {
  constructor(readonly problem: Problem) {
    super(problem.detail ?? problem.title);
  }

  get status() {
    return this.problem.status;
  }
}

/** The page's session: GET /me, or POST /sessions when there is none (architecture.md "Security"). The cookie is
 * HttpOnly; what the page keeps is the CSRF token it sends as X-CSRF-Token on every change. */
let session: Promise<string> | undefined;

async function openSession(): Promise<string> {
  const me = await globalThis.fetch(`${location.origin}/api/v1/me`);
  const res = me.status === 401 ? await globalThis.fetch(`${location.origin}/api/v1/sessions`, { method: "POST" }) : me;
  if (!res.ok) throw new Error(`cannot sign in: ${String(res.status)}`);
  return ((await res.json()) as Me).csrf;
}

export function ensureSession(): Promise<string> {
  session ??= openSession().catch((err: unknown) => {
    session = undefined; // the next call tries again
    throw err;
  });
  return session;
}

/** Tests start each case signed out. */
export function forgetSession() {
  session = undefined;
}

const changes = new Set(["POST", "PUT", "PATCH", "DELETE"]);

const sessionMiddleware: Middleware = {
  async onRequest({ request, schemaPath }) {
    if (!schemaPath.startsWith("/api/") || schemaPath === "/api/v1/sessions") return request;
    const csrf = await ensureSession();
    if (changes.has(request.method)) request.headers.set("X-CSRF-Token", csrf);
    return request;
  },
  async onResponse({ request, response, schemaPath }) {
    // An expired session: sign in again and repeat a read once (a change is left to the caller)
    if (response.status !== 401 || !schemaPath.startsWith("/api/") || request.method !== "GET") return response;
    session = undefined;
    await ensureSession();
    return globalThis.fetch(request);
  },
};
api.use(sessionMiddleware);

/** Query keys, one place, so SSE events can invalidate them later. */
export const keys = {
  readiness: ["readyz"] as const,
  courses: ["courses"] as const,
  course: (course: string) => ["courses", course] as const,
  item: (course: string, item: string) => ["courses", course, "items", item] as const,
  quizAttempts: (course: string, item: string) => ["courses", course, "items", item, "quiz-attempts"] as const,
  currentAttempt: ["attempts", "current"] as const,
  labState: ["lab", "state"] as const,
  workspace: (workspace: string) => ["workspaces", workspace] as const,
};

function fail(error: unknown): never {
  throw error instanceof Error ? error : new ProblemError(error as Problem);
}

export const queries = {
  courses: () => ({
    queryKey: keys.courses,
    queryFn: async () => {
      const { data, error } = await api.GET("/api/v1/courses");
      return data?.courses ?? fail(error);
    },
  }),
  course: (course: string) => ({
    queryKey: keys.course(course),
    queryFn: async () => {
      const { data, error } = await api.GET("/api/v1/courses/{course}", { params: { path: { course } } });
      return data ?? fail(error);
    },
  }),
  item: (course: string, item: string) => ({
    queryKey: keys.item(course, item),
    queryFn: async () => {
      const { data, error } = await api.GET("/api/v1/courses/{course}/items/{item}", {
        params: { path: { course, item } },
      });
      return data ?? fail(error);
    },
  }),
  quizAttempts: (course: string, item: string) => ({
    queryKey: keys.quizAttempts(course, item),
    queryFn: async () => {
      const { data, error } = await api.GET("/api/v1/courses/{course}/items/{item}/quiz-attempts", {
        params: { path: { course, item } },
      });
      return data?.attempts ?? fail(error);
    },
  }),
  /** The learner's lab in setup or running, or null. */
  currentAttempt: () => ({
    queryKey: keys.currentAttempt,
    queryFn: async () => {
      const { data, error } = await api.GET("/api/v1/attempts/current");
      return data ? data.attempt : fail(error);
    },
  }),
  workspaceTree: (workspace: string) => ({
    queryKey: keys.workspace(workspace),
    queryFn: async () => {
      const { data, error } = await api.GET("/api/v1/workspaces/{workspace}/tree", {
        params: { path: { workspace } },
      });
      return data ?? fail(error);
    },
  }),
  workspaceFile: (workspace: string, path: string) => ({
    queryKey: [...keys.workspace(workspace), "files", path] as const,
    queryFn: async () => {
      const { data, error } = await api.GET("/api/v1/workspaces/{workspace}/files", {
        params: { path: { workspace }, query: { path } },
      });
      return data ?? fail(error);
    },
  }),
  labState: () => ({
    queryKey: keys.labState,
    queryFn: async () => {
      const { data, error } = await api.GET("/api/v1/lab/state");
      return data ?? fail(error);
    },
  }),
};

/** Changes, for useMutation: each resolves with the API's answer or rejects with a ProblemError. */
export const actions = {
  startQuiz: async ({ course, item }: { course: string; item: string }) => {
    const { data, error } = await api.POST("/api/v1/courses/{course}/items/{item}/quiz-attempts", {
      params: { path: { course, item } },
    });
    return data ?? fail(error);
  },
  quizAttempt: async (attempt: number) => {
    const { data, error } = await api.GET("/api/v1/quiz-attempts/{attempt}", { params: { path: { attempt } } });
    return data ?? fail(error);
  },
  submitQuiz: async ({ attempt, answers }: { attempt: number; answers: Record<string, number[]> }) => {
    const { data, error } = await api.POST("/api/v1/quiz-attempts/{attempt}/submit", {
      params: { path: { attempt } },
      body: { answers },
    });
    return data ?? fail(error);
  },
  /** Starts a lab: the attempt, and the operation running its setup (202). */
  startLab: async ({ lab, course, item }: { lab: string; course: string; item: string }) => {
    const { data, error } = await api.POST("/api/v1/labs/{lab}/attempts", {
      params: { path: { lab } },
      body: { course, item },
    });
    return data ?? fail(error);
  },
  labAttempt: async (attempt: number) => {
    const { data, error } = await api.GET("/api/v1/attempts/{attempt}", { params: { path: { attempt } } });
    return data ?? fail(error);
  },
  checkStep: async ({ attempt, step, answer }: { attempt: number; step: string; answer?: number }) => {
    const { data, error } = await api.POST("/api/v1/attempts/{attempt}/checks", {
      params: { path: { attempt } },
      body: answer === undefined ? { step } : { step, answer },
    });
    return data ?? fail(error);
  },
  openHint: async ({ attempt, step }: { attempt: number; step: string }) => {
    const { data, error } = await api.POST("/api/v1/attempts/{attempt}/hints", {
      params: { path: { attempt } },
      body: { step },
    });
    return data ?? fail(error);
  },
  resetLab: async (attempt: number) => {
    const { data, error } = await api.POST("/api/v1/attempts/{attempt}/reset", { params: { path: { attempt } } });
    return data ?? fail(error);
  },
  abandonLab: async (attempt: number) => {
    const { data, error } = await api.POST("/api/v1/attempts/{attempt}/abandon", { params: { path: { attempt } } });
    return data ?? fail(error);
  },
  operation: async (operation: string) => {
    const { data, error } = await api.GET("/api/v1/operations/{operation}", { params: { path: { operation } } });
    return data ?? fail(error);
  },
  /** Saves a file; with base, only over that version (409 when the terminal changed it since). */
  saveFile: async ({
    workspace,
    path,
    content,
    base,
  }: {
    workspace: string;
    path: string;
    content: string;
    base?: string;
  }) => {
    const { data, error } = await api.PUT("/api/v1/workspaces/{workspace}/files", {
      params: { path: { workspace }, query: { path } },
      body: { content, ...(base === undefined ? {} : { base_sha256: base }) },
    });
    return data ?? fail(error);
  },
  deleteFile: async ({ workspace, path }: { workspace: string; path: string }) => {
    const { error } = await api.DELETE("/api/v1/workspaces/{workspace}/files", {
      params: { path: { workspace }, query: { path } },
    });
    if (error) fail(error);
  },
  moveFile: async ({ workspace, from, to }: { workspace: string; from: string; to: string }) => {
    const { error } = await api.POST("/api/v1/workspaces/{workspace}/moves", {
      params: { path: { workspace } },
      body: { from, to },
    });
    if (error) fail(error);
  },
  openTerminal: async (size: { workspace: string; cols: number; rows: number }) => {
    const { data, error } = await api.POST("/api/v1/terminals", { body: size });
    return data?.session ?? fail(error);
  },
};

/** Whether a query failed because the thing is not in the catalogue (404). */
export function isNotFound(error: unknown) {
  return error instanceof ProblemError && error.status === 404;
}
