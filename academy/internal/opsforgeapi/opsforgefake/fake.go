// Package opsforgefake is OpsForge's HTTP API in memory (academy/contract/openapi.yaml), for Academy's tests and
// the e2e suite (cmd/opsforge-fake): an empty lab by default, sandboxes whose lab work is scripted, their
// workspaces in memory and a terminal that echoes what is typed. It answers as `opsforge api serve` does: the
// token on every route but /v1/version, problem documents, jobs that are running when started and done when polled.
package opsforgefake

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"

	"github.com/IhebBelhadj/opsforge-academy/academy/internal/lab"
	"github.com/IhebBelhadj/opsforge-academy/academy/internal/workspace"
	"github.com/IhebBelhadj/opsforge-academy/academy/internal/workspace/memstore"
)

// Fake is the API; set the funcs to script the lab (nil: an empty lab, every step valid, work that succeeds).
type Fake struct {
	Token   string
	Version string
	Minor   int
	State   func() lab.State
	Steps   func(sets []json.RawMessage) []lab.StepError
	// Refuse refuses a sandboxInput before anything is made (nil: made)
	Refuse func(in json.RawMessage) *Refusal
	// Setup is a sandbox's setup (its sandboxInput; on reset too): the runOutput's error ("" when it ran), or a
	// failed job
	Setup func(in json.RawMessage) (string, error)
	Probe func(in json.RawMessage) (any, error) // the probeOutput document, or a failed job
	// Action answers an action (its actionInput) in sandbox, whose workspace is in Files: the runOutput document,
	// or a failed job (nil: every step ran, nothing measured)
	Action func(sandbox string, in json.RawMessage) (any, error)
	Files  *memstore.Store
	// Offline drops every connection unanswered: OpsForge out of reach
	Offline atomic.Bool

	mu        sync.Mutex
	jobs      map[string]*job
	sandboxes map[string]*sandbox
	made      int
	Calls     []string // "METHOD /path" of every authorized request
}

// Refusal is a problem answering POST /v1/sandboxes (lab-down, lab-busy, unmet-requirements with Unmet).
type Refusal struct {
	Slug   string
	Detail string
	Unmet  []string
}

// sandbox is one sandbox: its input and its document (sandboxOutput).
type sandbox struct {
	in  json.RawMessage
	doc map[string]any
}

// job is started running and reads as done (or failed) from the first poll on.
type job struct {
	ID      string
	Kind    string
	Sandbox string
	Log     []string
	result  map[string]any
}

// sandbox is the job's `sandbox`: its id, null for heal.
func (j *job) sandbox() any {
	if j.Sandbox == "" {
		return nil
	}
	return j.Sandbox
}

// New returns an empty lab answering for token.
func New(token string) *Fake {
	return &Fake{Token: token, Version: "1.2.0-fake", Minor: 2, Files: memstore.New(), jobs: map[string]*job{},
		sandboxes: map[string]*sandbox{}}
}

func problem(w http.ResponseWriter, status int, slug, detail string) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"contract": 1, "type": "/problems/" + slug, "title": slug,
		"detail": detail})
}

func send(w http.ResponseWriter, status int, doc map[string]any) {
	doc["contract"] = 1
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(doc)
}

// ServeHTTP routes as lab/apiserver.py does.
func (f *Fake) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if f.Offline.Load() {
		if conn, _, err := http.NewResponseController(w).Hijack(); err == nil {
			_ = conn.Close()
		}
		return
	}
	if r.Header.Get("Origin") != "" {
		problem(w, http.StatusForbidden, "cross-origin", "browsers cannot call this API")
		return
	}
	if r.URL.Path == "/v1/version" && r.Method == http.MethodGet {
		send(w, http.StatusOK, map[string]any{"minor": f.Minor, "opsforge": f.Version})
		return
	}
	if r.Header.Get("Authorization") != "Bearer "+f.Token {
		problem(w, http.StatusUnauthorized, "no-token", "send the token")
		return
	}
	f.mu.Lock()
	f.Calls = append(f.Calls, r.Method+" "+r.URL.Path)
	f.mu.Unlock()
	route := r.Method + " " + r.URL.Path
	switch {
	case route == "GET /v1/state":
		state := lab.State{Topology: "topology.yml", Sites: map[string]lab.Site{}, Links: map[string]lab.Link{},
			Nodes: []lab.Node{}, Placeholders: map[string]string{}}
		if f.State != nil {
			state = f.State()
		}
		data, _ := json.Marshal(state)
		var doc map[string]any
		_ = json.Unmarshal(data, &doc)
		send(w, http.StatusOK, doc)
	case route == "POST /v1/steps/validate":
		var in struct {
			Sets []json.RawMessage `json:"sets"`
		}
		if json.NewDecoder(r.Body).Decode(&in) != nil {
			problem(w, http.StatusBadRequest, "bad-input", "not JSON")
			return
		}
		errs := []lab.StepError{}
		if f.Steps != nil {
			errs = append(errs, f.Steps(in.Sets)...)
		}
		send(w, http.StatusOK, map[string]any{"errors": errs})
	case route == "POST /v1/heal":
		f.startJob(w, "heal", "", func() (any, error) { return map[string]any{"healed": false}, nil })
	case strings.HasPrefix(route, "GET /v1/jobs/"):
		f.getJob(w, strings.TrimPrefix(r.URL.Path, "/v1/jobs/"))
	case route == "GET /v1/sandboxes":
		f.mu.Lock()
		list := []map[string]any{}
		for _, sb := range f.sandboxes {
			list = append(list, sb.doc)
		}
		f.mu.Unlock()
		send(w, http.StatusOK, map[string]any{"sandboxes": list})
	case route == "POST /v1/sandboxes":
		f.create(w, r)
	case strings.HasPrefix(r.URL.Path, "/v1/sandboxes/"):
		f.inSandbox(w, r)
	default:
		problem(w, http.StatusNotFound, "not-found", "no route "+r.URL.Path)
	}
}

// startJob answers 202 with a job that reads as work's outcome from the first poll on.
func (f *Fake) startJob(w http.ResponseWriter, kind, sandbox string, work func() (any, error)) {
	out, err := work()
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	j := &job{ID: hex.EncodeToString(b), Kind: kind, Sandbox: sandbox, Log: []string{}}
	if err != nil {
		j.result = map[string]any{"status": "failed", "problem": map[string]any{"contract": 1,
			"type": "/problems/lab-error", "title": "Lab error", "detail": err.Error()}}
	} else {
		data, _ := json.Marshal(out)
		var doc map[string]any
		_ = json.Unmarshal(data, &doc)
		doc["contract"] = 1
		j.result = map[string]any{"status": "done", "output": doc}
	}
	f.mu.Lock()
	f.jobs[j.ID] = j
	f.mu.Unlock()
	w.Header().Set("Location", "/v1/jobs/"+j.ID)
	send(w, http.StatusAccepted, map[string]any{"id": j.ID, "kind": kind, "sandbox": j.sandbox(), "status": "running",
		"log": j.Log, "output": nil, "problem": nil})
}

func runOutput(errMsg string) map[string]any {
	doc := map[string]any{"contract": 1, "metrics": map[string]float64{}, "log": []string{}, "error": nil}
	if errMsg != "" {
		doc["error"] = errMsg
	}
	return doc
}

// create makes a sandbox: its workspace with the input's files, then its (scripted) setup as a job.
func (f *Fake) create(w http.ResponseWriter, r *http.Request) {
	raw, _ := io.ReadAll(r.Body)
	var in struct {
		Kind      string            `json:"kind"`
		Label     string            `json:"label"`
		Requires  map[string]any    `json:"requires"`
		Namespace bool              `json:"namespace"`
		Files     map[string]string `json:"files"`
	}
	if json.Unmarshal(raw, &in) != nil || in.Kind == "" || in.Label == "" {
		problem(w, http.StatusBadRequest, "bad-input", "not a sandboxInput")
		return
	}
	if f.Refuse != nil {
		if p := f.Refuse(raw); p != nil {
			w.Header().Set("Content-Type", "application/problem+json")
			w.WriteHeader(http.StatusConflict)
			doc := map[string]any{"contract": 1, "type": "/problems/" + p.Slug, "title": p.Slug, "detail": p.Detail}
			if p.Unmet != nil {
				doc["unmet"] = p.Unmet
			}
			_ = json.NewEncoder(w).Encode(doc)
			return
		}
	}
	f.mu.Lock()
	f.made++
	id := fmt.Sprintf("%08x", f.made) // as opaque as OpsForge's, and predictable in tests
	f.mu.Unlock()
	ctx := r.Context()
	_ = f.Files.Create(ctx, id)
	for path, text := range in.Files {
		_ = f.Files.Write(ctx, id, path, []byte(text))
	}
	var ns any
	placeholders := map[string]string{}
	if in.Namespace {
		ns = "sb-" + id
		placeholders["${ns}"] = "sb-" + id
	}
	if in.Requires == nil {
		in.Requires = map[string]any{}
	}
	now := time.Now().UTC()
	sb := &sandbox{in: raw, doc: map[string]any{"contract": 1, "id": id, "kind": in.Kind, "label": in.Label,
		"status": "creating", "namespace": ns, "layout": "stretched", "placeholders": placeholders,
		"requires": in.Requires, "setup": nil, "teardown": nil, "created_at": now.Format(time.RFC3339),
		"expires_at": now.Add(24 * time.Hour).Format(time.RFC3339)}}
	f.mu.Lock()
	f.sandboxes[id] = sb
	f.mu.Unlock()
	f.startJob(w, "create", id, func() (any, error) { return f.setUp(sb) })
}

// setUp runs a sandbox's scripted setup: ready, or failed (and cleaned up).
func (f *Fake) setUp(sb *sandbox) (any, error) {
	msg := ""
	if f.Setup != nil {
		var err error
		if msg, err = f.Setup(sb.in); err != nil {
			return nil, err
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	sb.doc["setup"], sb.doc["teardown"], sb.doc["status"] = runOutput(msg), nil, "ready"
	if msg != "" {
		sb.doc["status"], sb.doc["teardown"] = "failed", runOutput("")
	}
	return copyDoc(sb.doc), nil
}

func copyDoc(doc map[string]any) map[string]any {
	out := make(map[string]any, len(doc))
	for k, v := range doc {
		out[k] = v
	}
	return out
}

// inSandbox serves /v1/sandboxes/{id}[/...].
func (f *Fake) inSandbox(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.EscapedPath(), "/v1/sandboxes/")
	id, tail, _ := strings.Cut(rest, "/")
	f.mu.Lock()
	sb := f.sandboxes[id]
	status := ""
	if sb != nil {
		status, _ = sb.doc["status"].(string)
	}
	f.mu.Unlock()
	if sb == nil {
		problem(w, http.StatusNotFound, "not-found", "no sandbox "+id)
		return
	}
	ready := func() bool {
		if status != "ready" {
			problem(w, http.StatusConflict, "sandbox-state", "sandbox "+id+" is "+status)
			return false
		}
		return true
	}
	switch {
	case tail == "" && r.Method == http.MethodGet:
		f.mu.Lock()
		doc := copyDoc(sb.doc)
		f.mu.Unlock()
		send(w, http.StatusOK, doc)
	case tail == "" && r.Method == http.MethodDelete:
		f.startJob(w, "delete", id, func() (any, error) {
			f.mu.Lock()
			defer f.mu.Unlock()
			delete(f.sandboxes, id)
			_ = f.Files.Delete(id)
			doc := copyDoc(sb.doc)
			doc["status"], doc["teardown"] = "deleted", runOutput("")
			return doc, nil
		})
	case tail == "reset" && r.Method == http.MethodPost:
		if ready() {
			f.startJob(w, "reset", id, func() (any, error) { return f.setUp(sb) })
		}
	case tail == "probes" && r.Method == http.MethodPost:
		in, _ := io.ReadAll(r.Body)
		if ready() {
			f.startJob(w, "probe", id, func() (any, error) {
				if f.Probe != nil {
					return f.Probe(in)
				}
				return map[string]any{"passed": true, "results": []any{}, "log": []string{}, "error": nil}, nil
			})
		}
	case tail == "actions" && r.Method == http.MethodPost:
		in, _ := io.ReadAll(r.Body)
		if ready() {
			f.startJob(w, "action", id, func() (any, error) {
				if f.Action != nil {
					return f.Action(id, in)
				}
				return runOutput(""), nil
			})
		}
	case tail == "terminal" && r.Method == http.MethodGet:
		f.terminal(w, r, id)
	default:
		f.files(w, r, id, tail)
	}
}

func (f *Fake) getJob(w http.ResponseWriter, id string) {
	f.mu.Lock()
	j := f.jobs[id]
	f.mu.Unlock()
	if j == nil {
		problem(w, http.StatusNotFound, "not-found", "no job "+id)
		return
	}
	doc := map[string]any{"id": j.ID, "kind": j.Kind, "sandbox": j.sandbox(), "status": j.result["status"],
		"log": j.Log, "output": j.result["output"], "problem": j.result["problem"]}
	send(w, http.StatusOK, doc)
}

// files serves a sandbox's workspace (tail: files[/<path>] or moves).
func (f *Fake) files(w http.ResponseWriter, r *http.Request, ws, tail string) {
	ctx := r.Context()
	var err error
	switch {
	case tail == "files" && r.Method == http.MethodGet:
		var files []workspace.Entry
		if files, err = f.Files.List(ctx, ws); err == nil {
			list, total := []map[string]any{}, int64(0)
			for _, e := range files {
				list = append(list, map[string]any{"path": e.Path, "size": e.Size})
				total += e.Size
			}
			send(w, http.StatusOK, map[string]any{"files": list, "bytes": total})
			return
		}
	case tail == "moves" && r.Method == http.MethodPost:
		var in struct{ From, To string }
		_ = json.NewDecoder(r.Body).Decode(&in)
		if err = f.Files.Rename(ctx, ws, in.From, in.To); err == nil {
			w.WriteHeader(http.StatusNoContent)
			return
		}
	case strings.HasPrefix(tail, "files/"):
		p := strings.TrimPrefix(r.URL.Path, "/v1/sandboxes/"+ws+"/files/")
		if _, cerr := workspace.CleanPath(p); cerr != nil {
			problem(w, http.StatusBadRequest, "bad-path", p)
			return
		}
		switch r.Method {
		case http.MethodGet:
			var data []byte
			if data, err = f.Files.Read(ctx, ws, p); err == nil {
				w.Header().Set("Content-Type", "application/octet-stream")
				_, _ = w.Write(data) //nolint:gosec // octet-stream, as OpsForge serves files (a test fake)
				return
			}
		case http.MethodPut:
			data, _ := io.ReadAll(r.Body)
			if len(data) > workspace.MaxFile {
				problem(w, http.StatusRequestEntityTooLarge, "too-large", p)
				return
			}
			if err = f.Files.Write(ctx, ws, p, data); err == nil {
				w.WriteHeader(http.StatusNoContent)
				return
			}
		case http.MethodDelete:
			if err = f.Files.Remove(ctx, ws, p); err == nil {
				w.WriteHeader(http.StatusNoContent)
				return
			}
		}
	default:
		problem(w, http.StatusNotFound, "not-found", r.URL.Path)
		return
	}
	switch {
	case errors.Is(err, workspace.ErrNotFound):
		problem(w, http.StatusNotFound, "not-found", err.Error())
	case errors.Is(err, workspace.ErrExists):
		problem(w, http.StatusConflict, "exists", err.Error())
	default:
		problem(w, http.StatusInternalServerError, "internal", fmt.Sprint(err))
	}
}

// terminal is a sandbox's shell: it echoes what is typed (as a tty does), answers `stty size` with the window size and
// closes (1000) on `exit`; its banner is the real shell's ("Lab terminal in <dir>").
func (f *Fake) terminal(w http.ResponseWriter, r *http.Request, ws string) {
	q := r.URL.Query()
	cols, rows := q.Get("cols"), q.Get("rows")
	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	ctx := context.Background()
	out := func(s string) { _ = conn.Write(ctx, websocket.MessageBinary, []byte(s)) }
	out("Lab terminal in /workspaces/" + ws + ". Lab commands start with opsforge\r\n$ ")
	var line strings.Builder
	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			return
		}
		var frame struct {
			T    string `json:"t"`
			D    string `json:"d"`
			Cols int    `json:"cols"`
			Rows int    `json:"rows"`
		}
		if json.Unmarshal(data, &frame) != nil {
			continue
		}
		if frame.T == "r" {
			cols, rows = fmt.Sprint(frame.Cols), fmt.Sprint(frame.Rows)
			continue
		}
		for _, c := range frame.D {
			if c != '\r' && c != '\n' {
				line.WriteRune(c)
				out(string(c)) // the tty's echo
				continue
			}
			out("\r\n")
			switch cmd := strings.TrimSpace(line.String()); cmd {
			case "exit":
				_ = conn.Close(websocket.StatusNormalClosure, "")
				return
			case "stty size":
				out(rows + " " + cols + "\r\n")
			}
			line.Reset()
			out("$ ")
		}
	}
}
