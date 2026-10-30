-- challenge: submitted solutions to coding challenges and their verdicts (content-model.md "Challenge"). result is
-- the judged outcome (the failing case, the program's output); xp the challenge's XP on the first accepted one.

-- +goose Up
CREATE TABLE challenge_submissions (
    id           bigint           GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    user_id      bigint           NOT NULL REFERENCES users (id),
    course_id    text             NOT NULL,
    item_id      text             NOT NULL,
    content_hash text             NOT NULL,
    language     text             NOT NULL,
    code         text             NOT NULL,
    verdict      text             NOT NULL,
    passed       bigint           NOT NULL,
    total        bigint           NOT NULL,
    seconds      double precision NOT NULL,
    xp           bigint           NOT NULL,
    result       jsonb            NOT NULL,
    submitted_at timestamptz      NOT NULL
);
CREATE INDEX challenge_submissions_item ON challenge_submissions (user_id, course_id, item_id);

-- +goose Down
DROP TABLE challenge_submissions;
