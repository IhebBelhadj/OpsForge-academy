-- The platform's database (PostgreSQL, docs/PLAN.md split step 8): every module's tables, squashed into one
-- baseline from the SQLite migrations of M0-M1 (nothing was released). Each module owns its tables (docs/
-- content-model.md "Database"); later changes are new migrations, released migrations are never edited.
-- Timestamps are timestamptz, ids identity columns, documents jsonb.

-- +goose Up

-- The database itself: one row, written when the database is created (readyz reports it).
CREATE TABLE instance (
    id         smallint    PRIMARY KEY CHECK (id = 1),
    created_at timestamptz NOT NULL
);

-- identity: the users and their browser sessions. A session row keeps the SHA-256 of the cookie's token, never the
-- token, so reading the database gives no session.
CREATE TABLE users (
    id         bigint      GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    name       text        NOT NULL UNIQUE,
    created_at timestamptz NOT NULL
);

CREATE TABLE sessions (
    token_hash text        PRIMARY KEY,
    user_id    bigint      NOT NULL REFERENCES users (id),
    csrf       text        NOT NULL,
    created_at timestamptz NOT NULL,
    expires_at timestamptz NOT NULL
);

CREATE INDEX sessions_expires_at ON sessions (expires_at);

-- catalog: every piece of imported content, one row per entry (pack, course, item, lecture, quiz, challenge, lab,
-- badge). doc is the entry's bundle document; the columns are what queries filter and sort on. Rows are never
-- deleted: removed content is retired (attempts and awards refer to it).
CREATE TABLE content (
    kind         text        NOT NULL CHECK (kind IN ('pack', 'course', 'item', 'lecture', 'quiz', 'challenge', 'lab',
                                                      'badge')),
    key          text        NOT NULL,
    pack_id      text        NOT NULL,
    course_id    text,
    position     bigint      NOT NULL,
    title        text        NOT NULL,
    content_hash text        NOT NULL,
    doc          jsonb       NOT NULL,
    retired      boolean     NOT NULL DEFAULT false,
    updated_at   timestamptz NOT NULL,
    PRIMARY KEY (kind, key)
);

CREATE INDEX content_course ON content (course_id, kind, position);

-- history: the learner's history, append-only. Every domain event lands here (kind "<thing>.<what happened>",
-- subject what it happened to, data a JSON object); badge rules are evaluated against it and the event stream
-- replays it after a reconnect (Last-Event-ID = id).
CREATE TABLE events (
    id      bigint      GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    user_id bigint      NOT NULL REFERENCES users (id),
    kind    text        NOT NULL,
    subject text        NOT NULL,
    data    jsonb       NOT NULL,
    at      timestamptz NOT NULL
);

CREATE INDEX events_user ON events (user_id, id);
CREATE INDEX events_user_kind ON events (user_id, kind, subject);

-- progress: what the learner did with each item that no other module records: when it was opened last (the resume
-- point) and a lecture's completion with the content hash it was completed on. A course's first completion is kept;
-- notes are one text per item.
CREATE TABLE item_progress (
    user_id        bigint      NOT NULL REFERENCES users (id),
    course_id      text        NOT NULL,
    item_id        text        NOT NULL,
    content_hash   text,
    completed_at   timestamptz,
    last_opened_at timestamptz,
    PRIMARY KEY (user_id, course_id, item_id)
);

CREATE TABLE course_completions (
    user_id      bigint      NOT NULL REFERENCES users (id),
    course_id    text        NOT NULL,
    completed_at timestamptz NOT NULL,
    PRIMARY KEY (user_id, course_id)
);

CREATE TABLE notes (
    user_id    bigint      NOT NULL REFERENCES users (id),
    course_id  text        NOT NULL,
    item_id    text        NOT NULL,
    body       text        NOT NULL,
    updated_at timestamptz NOT NULL,
    PRIMARY KEY (user_id, course_id, item_id)
);

-- quiz: attempts at quiz items. questions holds the attempt's drawn questions with their keys, so a quiz edited
-- during an attempt does not change it; pass and offered are the quiz's pass mark and xp when it started, xp what the
-- attempt earned (the first pass of an item only).
CREATE TABLE quiz_attempts (
    id           bigint      GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    user_id      bigint      NOT NULL REFERENCES users (id),
    course_id    text        NOT NULL,
    item_id      text        NOT NULL,
    content_hash text        NOT NULL,
    started_at   timestamptz NOT NULL,
    deadline     timestamptz,
    questions    jsonb       NOT NULL,
    pass         bigint      NOT NULL,
    offered      bigint      NOT NULL,
    submitted_at timestamptz,
    answers      jsonb,
    correct      bigint      NOT NULL DEFAULT 0,
    score        bigint      NOT NULL DEFAULT 0,
    passed       boolean     NOT NULL DEFAULT false,
    xp           bigint      NOT NULL DEFAULT 0
);

CREATE INDEX quiz_attempts_item ON quiz_attempts (user_id, course_id, item_id);

-- operations: long operations answered with 202 (a lab's setup, a check, a teardown). result is the module's
-- document; error is set when the operation could not finish.
CREATE TABLE operations (
    id          text        PRIMARY KEY,
    user_id     bigint      NOT NULL REFERENCES users (id),
    kind        text        NOT NULL,
    subject     text        NOT NULL,
    status      text        NOT NULL CHECK (status IN ('running', 'done', 'failed')),
    result      jsonb,
    error       text,
    created_at  timestamptz NOT NULL,
    finished_at timestamptz
);

CREATE INDEX operations_running ON operations (status) WHERE status = 'running';

-- workspace: who owns each workspace (a sandbox's, on the lab host).
CREATE TABLE workspaces (
    id         text        PRIMARY KEY,
    user_id    bigint      NOT NULL REFERENCES users (id),
    created_at timestamptz NOT NULL
);

-- labsession: lab attempts (each in an OpsForge sandbox), their step results and the hints shown. One active attempt
-- (setup or running) per user, by a partial unique index. step_results since the last reset (a reset deletes them;
-- the history keeps every check); hints_shown counts each hint once per attempt.
CREATE TABLE attempts (
    id           bigint      GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    user_id      bigint      NOT NULL REFERENCES users (id),
    lab_id       text        NOT NULL,
    course_id    text,
    item_id      text,
    content_hash text        NOT NULL,
    namespace    text,
    sandbox      text,
    status       text        NOT NULL CHECK (status IN ('setup', 'running', 'passed', 'abandoned', 'failed')),
    started_at   timestamptz NOT NULL,
    finished_at  timestamptz,
    hints_used   bigint      NOT NULL DEFAULT 0,
    seconds      bigint,
    xp           bigint,
    error        text
);

CREATE UNIQUE INDEX attempts_one_active ON attempts (user_id) WHERE status IN ('setup', 'running');
CREATE INDEX attempts_user_status ON attempts (user_id, status);

CREATE TABLE step_results (
    id         bigint      GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    attempt_id bigint      NOT NULL REFERENCES attempts (id),
    step_id    text        NOT NULL,
    passed     boolean     NOT NULL,
    detail     jsonb       NOT NULL,
    at         timestamptz NOT NULL
);

CREATE INDEX step_results_attempt ON step_results (attempt_id);

CREATE TABLE hints_shown (
    attempt_id bigint      NOT NULL REFERENCES attempts (id),
    step_id    text        NOT NULL,
    idx        bigint      NOT NULL,
    at         timestamptz NOT NULL,
    PRIMARY KEY (attempt_id, step_id, idx)
);

-- rewards: the badges a learner was awarded, once each. XP is not stored (it is read from the passes); an award stays
-- when its badge is retired.
CREATE TABLE user_badges (
    user_id    bigint      NOT NULL REFERENCES users (id),
    badge_id   text        NOT NULL,
    awarded_at timestamptz NOT NULL,
    PRIMARY KEY (user_id, badge_id)
);

-- legacy: the Phase 10 rows `academy migrate-legacy` imported, by kind (attempt, event) and their id in labs.db,
-- with the id they got here; a later run imports only the rows not listed.
CREATE TABLE legacy_rows (
    kind      text   NOT NULL CHECK (kind IN ('attempt', 'event')),
    legacy_id bigint NOT NULL,
    new_id    bigint NOT NULL,
    PRIMARY KEY (kind, legacy_id)
);

-- +goose Down
DROP TABLE legacy_rows;
DROP TABLE user_badges;
DROP TABLE hints_shown;
DROP TABLE step_results;
DROP TABLE attempts;
DROP TABLE workspaces;
DROP TABLE operations;
DROP TABLE quiz_attempts;
DROP TABLE notes;
DROP TABLE course_completions;
DROP TABLE item_progress;
DROP TABLE events;
DROP TABLE content;
DROP TABLE sessions;
DROP TABLE users;
DROP TABLE instance;
