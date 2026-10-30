// Package lab is the lab as the platform sees it: the documents of OpsForge's contract (academy/contract/, its
// pinned copy; docs/architecture.md "The OpsForge API"). Modules declare the ports they need over these types
// (labsession, labstate, compiler, challenge); internal/opsforgeapi implements them. Pure data, and the errors
// every adapter maps OpsForge's refusals to.
package lab

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

var (
	// ErrLabDown is lab work refused because the lab is down.
	ErrLabDown = errors.New("the lab is down")
	// ErrBusy is lab work refused because other lab work runs (one job at a time), or a sandbox that must run
	// alone (faults, cluster changes) would share the lab.
	ErrBusy = errors.New("the lab is busy")
	// ErrUnmet is a sandbox the running lab cannot host (*UnmetError says why).
	ErrUnmet = errors.New("the running lab cannot host this")
	// ErrGone is a sandbox OpsForge no longer has (deleted, expired, or cleaned up after a restart).
	ErrGone = errors.New("the sandbox is gone")
	// ErrOffline is lab work that cannot reach OpsForge (no answer, or no token to call it with): Academy serves its
	// courses without OpsForge, labs need it.
	ErrOffline = errors.New("OpsForge is out of reach")
)

// UnmetError is ErrUnmet with what the running lab lacks, one entry each ("layout multi-cluster (...)").
type UnmetError struct{ Unmet []string }

func (e *UnmetError) Error() string {
	return fmt.Sprintf("%v: %s", ErrUnmet, strings.Join(e.Unmet, "; "))
}

// Is makes errors.Is(err, ErrUnmet) true.
func (e *UnmetError) Is(target error) bool { return target == ErrUnmet }

// State is the lab now, as a mimic panel shows it (`academy state`).
type State struct {
	Up       bool            `json:"up"`
	Error    *string         `json:"error"`
	Topology string          `json:"topology"`
	Layout   *string         `json:"layout"`
	Sites    map[string]Site `json:"sites"`
	Links    map[string]Link `json:"links"`
	Nodes    []Node          `json:"nodes"`
	Fault    bool            `json:"fault"`
	// Placeholders are the values of the lab-text placeholders in the running topology (scenario.placeholders):
	// "${node:a}" → "a-cp1", "${ip:a-cp1}" → "10.10.0.11", ...
	Placeholders map[string]string `json:"placeholders"`
}

// Site is one region: its public address and VPC.
type Site struct {
	PublicIP string `json:"public_ip"`
	VPC      string `json:"vpc"`
}

// Link is a WAN link now (Live) and as topology.yml has it (Desired); Condition is ok, changed or cut.
type Link struct {
	Live      LinkConditions `json:"live"`
	Desired   LinkConditions `json:"desired"`
	Condition string         `json:"condition"`
}

// LinkConditions are a link's netem settings.
type LinkConditions struct {
	RTT    string `json:"rtt"`
	Jitter string `json:"jitter,omitempty"`
	Loss   string `json:"loss,omitempty"`
	Rate   string `json:"rate,omitempty"`
	MTU    int    `json:"mtu,omitempty"`
}

// Node is one lab instance.
type Node struct {
	Name   string `json:"name"`
	Site   string `json:"site"`
	Role   string `json:"role"`
	Status string `json:"status"`
}

// Requires is what a sandbox needs from the running lab: the course's layout and needs; Faults when its setup
// breaks links (deleting it heals the lab), ClusterChanges when it changes the cluster outside its namespace. Either
// of those makes it run alone. Runtimes are the program runtimes it runs code with (contract minor 2).
type Requires struct {
	Layout         *string  `json:"layout,omitempty"`
	Needs          []string `json:"needs,omitempty"`
	Faults         bool     `json:"faults,omitempty"`
	ClusterChanges bool     `json:"cluster_changes,omitempty"`
	Runtimes       []string `json:"runtimes,omitempty"`
}

// SandboxRequest is one attempt's sandbox (sandboxInput): Kind "lab" or "challenge", a Label for the lab's owner,
// a Namespace (sb-<id>, `${ns}`) when asked, the workspace's first Files (path → text) and the steps OpsForge runs
// to set it up and, when it deletes it, to tear it down. Steps are scenario steps as the bundle holds them.
// ExpiresAfter is how long OpsForge keeps it without a call (seconds; 0: OpsForge's default, a day).
type SandboxRequest struct {
	Kind         string            `json:"kind"`
	Label        string            `json:"label"`
	Requires     Requires          `json:"requires"`
	Namespace    bool              `json:"namespace,omitempty"`
	Files        map[string]string `json:"files,omitempty"`
	Setup        []json.RawMessage `json:"setup,omitempty"`
	Teardown     []json.RawMessage `json:"teardown,omitempty"`
	ExpiresAfter int               `json:"expires_after,omitempty"`
}

// The statuses of a sandbox that lab work ends with.
const (
	SandboxReady   = "ready"
	SandboxFailed  = "failed" // its setup failed; OpsForge already cleaned it up (Teardown)
	SandboxDeleted = "deleted"
)

// Sandbox is one as OpsForge reports it (sandboxOutput). Setup is the last setup's result; Teardown the cleanup's
// (after a failed setup, or when deleted).
type Sandbox struct {
	ID           string            `json:"id"`
	Status       string            `json:"status"`
	Namespace    *string           `json:"namespace"`
	Placeholders map[string]string `json:"placeholders"`
	Setup        *RunResult        `json:"setup"`
	Teardown     *RunResult        `json:"teardown"`
}

// Pending is a sandbox being made: its id (known at once) and the job making it.
type Pending struct {
	Sandbox string
	Job     string
}

// RunResult is what the steps measured. Error says why the run stopped early ("" when every step ran). Outputs
// are the named program steps' outputs (contract minor 2; actions only).
type RunResult struct {
	Metrics map[string]float64 `json:"metrics"`
	Outputs map[string]string  `json:"outputs,omitempty"`
	Log     []string           `json:"log"`
	Error   string             `json:"error"`
}

// ProbeRequest is a probe check, run in a sandbox: steps, then expectations on their metrics. Expect stays raw so
// its order (the order of the results) is the author's.
type ProbeRequest struct {
	Steps  []json.RawMessage `json:"steps"`
	Expect json.RawMessage   `json:"expect"`
}

// ProbeResult is a check's verdict, one Expectation per condition. The probe stops at the first failing named step,
// so expectations on later steps are not Checked.
type ProbeResult struct {
	Passed  bool          `json:"passed"`
	Results []Expectation `json:"results"`
	Log     []string      `json:"log"`
	Error   string        `json:"error"`
}

// Expectation is one condition on one metric (Want: "<op> <value>"); Got is nil when nothing was measured.
type Expectation struct {
	Metric  string   `json:"metric"`
	Want    string   `json:"want"`
	Got     *float64 `json:"got"`
	Passed  bool     `json:"passed"`
	Checked bool     `json:"checked"`
}

// StepSet is scenario steps to check without running them (`api validate-steps`): a lab's setup or teardown, a
// check's probe with its Expect. NoFaults refuses fault steps (partition, degrade, ...): a check only observes the
// lab.
type StepSet struct {
	Steps    []any
	Expect   map[string]any
	NoFaults bool
}

// MarshalJSON writes the contract's stepsInput set: `faults: false` only when refused, `expect` only when given.
func (s StepSet) MarshalJSON() ([]byte, error) {
	doc := map[string]any{"steps": s.Steps}
	if s.Steps == nil {
		doc["steps"] = []any{}
	}
	if s.Expect != nil {
		doc["expect"] = s.Expect
	}
	if s.NoFaults {
		doc["faults"] = false
	}
	return json.Marshal(doc)
}

// StepError is why the steps of the Set-th StepSet would not run ("steps[2]: ..." or "expect: <metric>: ...").
type StepError struct {
	Set     int    `json:"set"`
	Message string `json:"message"`
}
