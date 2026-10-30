package challenge

import (
	"context"
	"encoding/json"

	"github.com/IhebBelhadj/opsforge-academy/academy/internal/catalog"
	"github.com/IhebBelhadj/opsforge-academy/academy/internal/lab"
	"github.com/IhebBelhadj/opsforge-academy/academy/internal/operations"
	"github.com/IhebBelhadj/opsforge-academy/academy/internal/platform"
)

// Repo stores submissions.
type Repo interface {
	// Create stores a judged submission and returns its id.
	Create(ctx context.Context, s Submission) (int64, error)
	// Submissions are user's submissions to an item, newest first.
	Submissions(ctx context.Context, user int64, course, item string) ([]Submission, error)
	// Accepted are user's accepted submissions, oldest first.
	Accepted(ctx context.Context, user int64) ([]Submission, error)
}

// Content reads an item with the entry holding its cases and solution (catalog.Service).
type Content interface {
	Content(ctx context.Context, course, id string) (catalog.ItemRef, catalog.Entry, error)
}

// Sandboxes runs code on OpsForge (opsforgeapi.Sandboxes): one sandbox per run or submission.
type Sandboxes interface {
	Create(ctx context.Context, req lab.SandboxRequest) (lab.Pending, error)
	Await(ctx context.Context, p lab.Pending) (lab.Sandbox, error)
	Act(ctx context.Context, id string, steps []json.RawMessage) (lab.RunResult, error)
	Delete(ctx context.Context, id string) (lab.Sandbox, error)
}

// Operations runs the judging in the background (operations.Service).
type Operations interface {
	Start(ctx context.Context, user int64, kind, subject string, work operations.Work) (operations.Operation, error)
}

// Publisher appends to the history (history.Service).
type Publisher interface {
	Publish(ctx context.Context, e platform.Event) (platform.Event, error)
}
