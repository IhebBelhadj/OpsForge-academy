package opsforgeapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"

	"github.com/IhebBelhadj/opsforge-academy/academy/internal/lab"
)

// Sandboxes is where attempts' lab work happens on OpsForge (`/v1/sandboxes`): OpsForge makes each one from the
// attempt's requirements (checking them against the running lab), runs its steps in its workspace and cleans it up.
// Refusals come back as the lab package's errors.
type Sandboxes struct {
	c *Client
}

// Sandboxes returns the sandboxes' adapter.
func (c *Client) Sandboxes() Sandboxes { return Sandboxes{c: c} }

func sandboxPath(id string, rest ...string) string {
	p := "/v1/sandboxes/" + url.PathEscape(id)
	for _, r := range rest {
		p += "/" + r
	}
	return p
}

// labErr turns OpsForge's refusals of lab work into the lab package's errors.
func labErr(err error) error {
	var p *Problem
	if !errors.As(err, &p) {
		return err
	}
	switch {
	case p.Slug() == "lab-down":
		return fmt.Errorf("%w: %s", lab.ErrLabDown, p.Detail)
	case p.Slug() == "lab-busy":
		return fmt.Errorf("%w: %s", lab.ErrBusy, p.Detail)
	case p.Slug() == "unmet-requirements":
		return &lab.UnmetError{Unmet: p.Unmet}
	case p.Status == http.StatusNotFound:
		return fmt.Errorf("%w: %s", lab.ErrGone, p.Detail)
	}
	return err
}

// Create starts making a sandbox (`POST /v1/sandboxes`, a job): refused at once when the lab is down, busy or
// lacks what it requires; its id is known from then on. Await its setup.
func (s Sandboxes) Create(ctx context.Context, req lab.SandboxRequest) (lab.Pending, error) {
	ctx, cancel := context.WithTimeout(ctx, s.c.Timeout)
	defer cancel()
	j, err := s.c.start(ctx, http.MethodPost, "/v1/sandboxes", struct {
		Contract int `json:"contract"`
		lab.SandboxRequest
	}{Contract, req})
	if err != nil {
		return lab.Pending{}, labErr(err)
	}
	if j.Sandbox == nil {
		return lab.Pending{}, fmt.Errorf("opsforge create job %s: no sandbox", j.ID)
	}
	return lab.Pending{Sandbox: *j.Sandbox, Job: j.ID}, nil
}

// Await waits for a sandbox's setup: the sandbox, ready or failed (then already cleaned up).
func (s Sandboxes) Await(ctx context.Context, p lab.Pending) (lab.Sandbox, error) {
	ctx, cancel := context.WithTimeout(ctx, s.c.LabTimeout)
	defer cancel()
	var sb lab.Sandbox
	return sb, labErr(s.c.await(ctx, jobDoc{ID: p.Job, Kind: "create", Status: "running"}, "sandboxOutput", &sb))
}

// Probe runs a probe check in a ready sandbox (a job).
func (s Sandboxes) Probe(ctx context.Context, id string, req lab.ProbeRequest) (lab.ProbeResult, error) {
	var res lab.ProbeResult
	return res, labErr(s.c.job(ctx, http.MethodPost, sandboxPath(id, "probes"), struct {
		Contract int `json:"contract"`
		lab.ProbeRequest
	}{Contract, req}, "probeOutput", &res))
}

// Reset runs a ready sandbox's teardown, then its setup again (a job; the files are kept).
func (s Sandboxes) Reset(ctx context.Context, id string) (lab.Sandbox, error) {
	var sb lab.Sandbox
	return sb, labErr(s.c.job(ctx, http.MethodPost, sandboxPath(id, "reset"), nil, "sandboxOutput", &sb))
}

// Delete removes a sandbox (a job): its terminals hung up, its teardown, its namespace, a heal when it had faults,
// its files. lab.ErrGone when OpsForge has none by that id.
func (s Sandboxes) Delete(ctx context.Context, id string) (lab.Sandbox, error) {
	var sb lab.Sandbox
	return sb, labErr(s.c.job(ctx, http.MethodDelete, sandboxPath(id), nil, "sandboxOutput", &sb))
}
