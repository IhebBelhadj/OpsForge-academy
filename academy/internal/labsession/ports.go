package labsession

import (
	"context"
	"encoding/json"
	"time"

	"github.com/IhebBelhadj/opsforge-academy/academy/internal/catalog"
	"github.com/IhebBelhadj/opsforge-academy/academy/internal/lab"
	"github.com/IhebBelhadj/opsforge-academy/academy/internal/operations"
	"github.com/IhebBelhadj/opsforge-academy/academy/internal/platform"
)

// Repo stores attempts, their step results and the hints shown.
type Repo interface {
	// Create stores a new attempt and returns its id; ErrAnotherLabRunning if the user has an active one.
	Create(ctx context.Context, a Attempt) (int64, error)
	// SetPlaces records the attempt's namespace and workspace (its sandbox: the id names both).
	SetPlaces(ctx context.Context, id int64, namespace, workspace string) error
	// Attempt reads an attempt, or ErrAttemptNotFound.
	Attempt(ctx context.Context, id int64) (Attempt, error)
	// Active is the user's attempt in setup or running, if any.
	Active(ctx context.Context, user int64) (Attempt, bool, error)
	// Finish records an attempt's status, end, time, XP and error.
	Finish(ctx context.Context, a Attempt) error
	// PassedSteps are the attempt's passed steps since it was last reset.
	PassedSteps(ctx context.Context, id int64) (map[string]bool, error)
	RecordStep(ctx context.Context, id int64, step string, passed bool, detail json.RawMessage, at time.Time) error
	// ClearSteps forgets the attempt's step results (a reset).
	ClearSteps(ctx context.Context, id int64) error
	// Hints are how many hints of each step the attempt has shown.
	Hints(ctx context.Context, id int64) (map[string]int, error)
	// ShowHint records hint idx of a step as shown and counts it in the attempt's hints_used, once.
	ShowHint(ctx context.Context, id int64, step string, idx int, at time.Time) error
	// Passed are the user's passed attempts, oldest first.
	Passed(ctx context.Context, user int64) ([]Attempt, error)
	// Recover moves attempts left in setup by a stopped process to running (they can be abandoned).
	Recover(ctx context.Context) (int64, error)
}

// Catalog reads entries as imported (catalog.Service).
type Catalog interface {
	Entry(ctx context.Context, kind catalog.Kind, key string) (catalog.Entry, error)
}

// Sandboxes are where attempts' lab work happens (opsforgeapi.Sandboxes): OpsForge makes one per attempt from the
// lab's requirements and cleans it up. Refusals are the lab package's errors (lab.ErrLabDown, lab.ErrBusy,
// *lab.UnmetError; lab.ErrGone for a sandbox it no longer has).
type Sandboxes interface {
	// Create starts making a sandbox: refused at once, else its id is known; Await waits for its setup.
	Create(ctx context.Context, req lab.SandboxRequest) (lab.Pending, error)
	Await(ctx context.Context, p lab.Pending) (lab.Sandbox, error)
	Probe(ctx context.Context, id string, req lab.ProbeRequest) (lab.ProbeResult, error)
	// Reset runs the teardown, then the setup again.
	Reset(ctx context.Context, id string) (lab.Sandbox, error)
	// Delete runs the teardown and removes the sandbox (healing the lab when it had faults).
	Delete(ctx context.Context, id string) (lab.Sandbox, error)
}

// Workspaces records whose an attempt's workspace (its sandbox's) is (workspace.Service).
type Workspaces interface {
	Claim(ctx context.Context, user int64, ws string) error
}

// Operations runs the long part of a request in the background (operations.Service).
type Operations interface {
	Start(ctx context.Context, user int64, kind, subject string, work operations.Work) (operations.Operation, error)
}

// Publisher appends to the history (history.Service).
type Publisher interface {
	Publish(ctx context.Context, e platform.Event) (platform.Event, error)
}
