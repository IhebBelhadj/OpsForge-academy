package httpapi_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/IhebBelhadj/opsforge-academy/academy/internal/platform/postgres/pgtest"

	"github.com/IhebBelhadj/opsforge-academy/academy/internal/catalog"
	catalogpg "github.com/IhebBelhadj/opsforge-academy/academy/internal/catalog/postgres"
	"github.com/IhebBelhadj/opsforge-academy/academy/internal/challenge"
	challengepg "github.com/IhebBelhadj/opsforge-academy/academy/internal/challenge/postgres"
	"github.com/IhebBelhadj/opsforge-academy/academy/internal/contentimport"
	"github.com/IhebBelhadj/opsforge-academy/academy/internal/history"
	historypg "github.com/IhebBelhadj/opsforge-academy/academy/internal/history/postgres"
	"github.com/IhebBelhadj/opsforge-academy/academy/internal/httpapi"
	"github.com/IhebBelhadj/opsforge-academy/academy/internal/identity"
	identitypg "github.com/IhebBelhadj/opsforge-academy/academy/internal/identity/postgres"
	"github.com/IhebBelhadj/opsforge-academy/academy/internal/lab"
	"github.com/IhebBelhadj/opsforge-academy/academy/internal/labsession"
	labpg "github.com/IhebBelhadj/opsforge-academy/academy/internal/labsession/postgres"
	"github.com/IhebBelhadj/opsforge-academy/academy/internal/labstate"
	"github.com/IhebBelhadj/opsforge-academy/academy/internal/operations"
	operationspg "github.com/IhebBelhadj/opsforge-academy/academy/internal/operations/postgres"
	"github.com/IhebBelhadj/opsforge-academy/academy/internal/opsforgeapi"
	"github.com/IhebBelhadj/opsforge-academy/academy/internal/opsforgeapi/opsforgefake"
	"github.com/IhebBelhadj/opsforge-academy/academy/internal/platform"
	"github.com/IhebBelhadj/opsforge-academy/academy/internal/platform/postgres"
	"github.com/IhebBelhadj/opsforge-academy/academy/internal/progress"
	progresspg "github.com/IhebBelhadj/opsforge-academy/academy/internal/progress/postgres"
	"github.com/IhebBelhadj/opsforge-academy/academy/internal/quiz"
	quizpg "github.com/IhebBelhadj/opsforge-academy/academy/internal/quiz/postgres"
	"github.com/IhebBelhadj/opsforge-academy/academy/internal/rewards"
	rewardspg "github.com/IhebBelhadj/opsforge-academy/academy/internal/rewards/postgres"
	"github.com/IhebBelhadj/opsforge-academy/academy/internal/terminal"
	"github.com/IhebBelhadj/opsforge-academy/academy/internal/workspace"
	workspacepg "github.com/IhebBelhadj/opsforge-academy/academy/internal/workspace/postgres"
)

// world is the modules that keep learner state, on one temporary database (their tables refer to users) holding
// the golden bundle's catalogue, with the real event bus and hub.
type world struct {
	store    *postgres.Store
	hub      *platform.Hub
	history  *history.Service
	catalog  *catalog.Service
	progress *progress.Service
	quizzes  *quiz.Service
	code     *challenge.Service
	ops      *operations.Service
	spaces   *workspace.Service
	labs     *labsession.Service
	rewards  *rewards.Service
	state    *labstate.Service
	lab      *scriptedLab
	// OpsForge in memory: the workspaces and terminals (lab work is the scripted lab's)
	opsforge  *opsforgefake.Fake
	client    *opsforgeapi.Client
	terminals terminal.Shells
}

// scriptedLab is the lab's state as the HTTP tests see it: up, with a cut link, unless stateErr. Its sandboxes are
// the fake OpsForge's (w.opsforge: failProbes, refuse).
type scriptedLab struct {
	mu       sync.Mutex
	stateErr error
}

func (l *scriptedLab) State(context.Context) (lab.State, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.stateErr != nil {
		return lab.State{}, l.stateErr
	}
	layout := "stretched"
	return lab.State{Up: true, Topology: "topology.yml", Layout: &layout,
		Sites: map[string]lab.Site{"a": {PublicIP: "203.0.113.10", VPC: "10.10.0.0/24"}},
		Links: map[string]lab.Link{"a-b": {Live: lab.LinkConditions{RTT: "40ms", Loss: "100%"},
			Desired: lab.LinkConditions{RTT: "40ms", Loss: "0.1%"}, Condition: "cut"}},
		Nodes:        []lab.Node{{Name: "a-cp1", Site: "a", Role: "k3s-server", Status: "Running"}},
		Fault:        true,
		Placeholders: map[string]string{"${node:c}": "c-w1", "${public:a}": "203.0.113.10"}}, nil
}

// failProbes makes the fake OpsForge's probes fail (nodes.exit got 1) or pass.
func (w *world) failProbes(failing bool) {
	w.opsforge.Probe = func(json.RawMessage) (any, error) {
		got := 0.0
		if failing {
			got = 1
		}
		return map[string]any{"passed": !failing, "results": []map[string]any{{"metric": "nodes.exit", "want": "== 0",
			"got": got, "passed": !failing, "checked": true}}, "log": []string{}, "error": nil}, nil
	}
}

// quizLab is a lab with a quiz step (the golden pack has none), added to the world's catalogue.
const quizLab = `{"id":"quiz-lab","title":"Quiz lab","xp":40,"files":{},"body":{"steps":[
{"id":"q","title":"Q","body":"?","hints":["think"],"check":{"quiz":{"question":"?","choices":["a","b"],"answer":1}}}]}}`

// wait lets the world's operations finish.
func (w *world) wait(t *testing.T) {
	t.Helper()
	if err := w.ops.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
}

// inOrder is a Random that keeps every order (questions and choices as in the quiz file).
type inOrder struct{}

func (inOrder) Perm(n int) []int {
	p := make([]int, n)
	for i := range p {
		p[i] = i
	}
	return p
}

func newWorld(t *testing.T) *world {
	t.Helper()
	return buildWorld(t, func(_ *testing.T, data []byte) []byte { return data })
}

// newCodeWorld is the world with sumChallenge in its catalogue.
func newCodeWorld(t *testing.T) *world {
	t.Helper()
	return buildWorld(t, withCodeChallenge)
}

// buildWorld makes the world on the golden bundle as content edits it.
func buildWorld(t *testing.T, content func(*testing.T, []byte) []byte) *world {
	t.Helper()
	store, err := postgres.Open(context.Background(), pgtest.URL(t), platform.SystemClock{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	hub := platform.NewHub()
	t.Cleanup(hub.Close)
	data, err := os.ReadFile("../../testdata/bundle.golden.json")
	if err != nil {
		t.Fatal(err)
	}
	entries, err := contentimport.Entries(content(t, data))
	if err != nil {
		t.Fatal(err)
	}
	entries = append(entries, catalog.Entry{Kind: catalog.KindLab, Key: "quiz-lab", PackID: "demo", Title: "Quiz lab",
		Hash: "QL", Doc: []byte(quizLab)})
	repo := catalogpg.NewRepo(store.DB)
	if err := repo.Apply(context.Background(), entries, nil, time.Now()); err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	clock := platform.SystemClock{}
	w := &world{store: store, hub: hub, catalog: catalog.NewService(repo),
		history: history.NewService(historypg.NewRepo(store.DB), hub, clock, log)}
	w.quizzes = quiz.NewService(quizpg.NewRepo(store.DB), w.catalog, w.history, inOrder{}, clock)
	// As cmd/academy adapts them
	quizzes := progress.SourceFunc(func(ctx context.Context, user int64) ([]progress.Completion, error) {
		passed, err := w.quizzes.Passed(ctx, user)
		out := make([]progress.Completion, len(passed))
		for i, a := range passed {
			out[i] = progress.Completion{Type: catalog.ItemQuiz, Ref: a.Course + "/" + a.Item, Hash: a.Hash,
				At: *a.SubmittedAt}
		}
		return out, err
	})
	w.ops = operations.NewService(operationspg.NewRepo(store.DB), hub, clock, log)
	w.opsforge, w.client = fakeOpsForge(t)
	w.terminals, _ = shells(t, w.client)
	w.spaces = workspace.NewService(w.client.Files(), workspacepg.NewRepo(store.DB), clock)
	w.lab = &scriptedLab{}
	w.labs = labsession.NewService(labpg.NewRepo(store.DB), w.catalog, w.client.Sandboxes(), w.spaces, w.ops,
		w.history, clock)
	w.code = challenge.NewService(challengepg.NewRepo(store.DB), w.catalog, w.client.Sandboxes(), w.ops, w.history,
		clock)
	w.opsforge.Action = w.judge
	t.Cleanup(func() { _ = w.ops.Wait(context.Background()) })
	labs := progress.SourceFunc(func(ctx context.Context, user int64) ([]progress.Completion, error) {
		passed, err := w.labs.Passed(ctx, user)
		out := make([]progress.Completion, len(passed))
		for i, a := range passed {
			out[i] = progress.Completion{Type: catalog.ItemLab, Ref: a.Lab, Hash: a.Hash, At: *a.FinishedAt}
		}
		return out, err
	})
	w.progress = progress.NewService(progresspg.NewRepo(store.DB), w.catalog, w.history, clock,
		progress.Sources{quizzes, labs})
	w.history.Subscribe(w.progress.OnEvent)
	ids := identity.NewService(identitypg.NewRepo(store.DB), clock)
	w.rewards = rewards.NewService(rewardspg.NewRepo(store.DB), w.catalog, rewardSources(w.labs, w.quizzes,
		w.progress, ids), w.history, clock, log)
	w.history.Subscribe(w.rewards.OnEvent)
	w.state = labstate.NewService(w.lab, clock)
	w.history.Subscribe(w.state.OnEvent)
	return w
}

// sumChallenge is a coding challenge (the golden pack has none) added to k8s-intro's section: add(a, b), two cases,
// one of them a sample.
const sumChallenge = `{"course":"k8s-intro","id":"sum","kind":"code","title":"Sum","difficulty":"easy","minutes":5,
"xp":30,"needs":["k3s"],"statement":"Add.","function":"add","params":["a","b"],"compare":"exact","samples":1,
"cases":[{"args":[1,2],"want":3},{"args":[20,22],"want":42}],"languages":["python","go"],
"files":["solution.py","solution.go"],"starter":{"solution.py":"def add(a, b):\n    pass\n","solution.go":"package main\n"},
"solution":{"solution.py":"def add(a, b):\n    return a + b\n","solution.go":"package main\n\nfunc Add(a, b int) int { return a + b }\n"},
"time_limit_s":10,"tests":[],"hints":[],"content_hash":"5u4c0de0000000aa"}`

// withCodeChallenge is the golden bundle with sumChallenge in its first course.
func withCodeChallenge(t *testing.T, data []byte) []byte {
	t.Helper()
	var b map[string]any
	if err := json.Unmarshal(data, &b); err != nil {
		t.Fatal(err)
	}
	var ch map[string]any
	if err := json.Unmarshal([]byte(sumChallenge), &ch); err != nil {
		t.Fatal(err)
	}
	pack := b["packs"].([]any)[0].(map[string]any)
	pack["challenges"] = append(pack["challenges"].([]any), ch)
	section := pack["courses"].([]any)[0].(map[string]any)["sections"].([]any)[0].(map[string]any)
	section["items"] = append(section["items"].([]any), map[string]any{"id": "sum", "type": "challenge", "ref": "sum",
		"title": "Sum", "minutes": 5, "content_hash": ch["content_hash"]})
	out, err := json.Marshal(b)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// judge is the fake OpsForge's program step for sumChallenge: it answers every case of tests.json with the sum of
// its arguments, or 0 when the solution says "wrong", and fails the step when it says "crash".
func (w *world) judge(sandbox string, _ json.RawMessage) (any, error) {
	ctx := context.Background()
	tests, err := w.opsforge.Files.Read(ctx, sandbox, "tests.json")
	if err != nil {
		return nil, err
	}
	code, _ := w.opsforge.Files.Read(ctx, sandbox, "solution.py")
	if strings.Contains(string(code), "crash") {
		return map[string]any{"metrics": map[string]float64{}, "outputs": map[string]string{}, "log": []string{},
			"error": "program: the pod did not start in 120s"}, nil
	}
	var doc struct{ Cases []struct{ Args []int } }
	if err := json.Unmarshal(tests, &doc); err != nil {
		return nil, err
	}
	var out strings.Builder
	out.WriteString("print from the program\n")
	for i, c := range doc.Cases {
		got := c.Args[0] + c.Args[1]
		if strings.Contains(string(code), "wrong") {
			got = 0
		}
		fmt.Fprintf(&out, "\n%s{\"i\":%d,\"got\":%d,\"ms\":0.5}\n", challenge.Mark, i, got)
	}
	return map[string]any{"metrics": map[string]float64{"judge.exit": 0, "judge.seconds": 0.3, "judge.timed_out": 0,
		"judge.oom": 0}, "outputs": map[string]string{"judge": out.String()}, "log": []string{}, "error": nil}, nil
}

// rewardSources reads the history for the rules from the modules, as cmd/academy adapts them.
func rewardSources(labs *labsession.Service, quizzes *quiz.Service, learning *progress.Service,
	ids *identity.Service) rewards.Sources {
	return rewards.Sources{
		Labs: func(ctx context.Context, user int64) ([]rewards.LabPass, error) {
			passed, err := labs.Passed(ctx, user)
			out := make([]rewards.LabPass, len(passed))
			for i, a := range passed {
				out[i] = rewards.LabPass{Lab: a.Lab, Hints: a.HintsUsed, Seconds: a.Seconds, XP: a.XP}
			}
			return out, err
		},
		Quizzes: func(ctx context.Context, user int64) ([]rewards.QuizPass, error) {
			passed, err := quizzes.Passed(ctx, user)
			out := make([]rewards.QuizPass, len(passed))
			for i, a := range passed {
				out[i] = rewards.QuizPass{Course: a.Course, Item: a.Item, XP: a.XP}
			}
			return out, err
		},
		Courses: learning.CompletedCourses,
		Users:   ids.UserIDs,
	}
}

// deps serves the world: identity on its database, the history and the event streams.
func (w *world) deps(d *httpapi.Deps) {
	d.Identity = identity.NewService(identitypg.NewRepo(w.store.DB), platform.SystemClock{})
	d.History = w.history
	d.Streams = w.hub
	d.Catalog = w.catalog
	d.Progress = w.progress
	d.Quizzes = w.quizzes
	d.Challenges = w.code
	d.Labs = w.labs
	d.Operations = w.ops
	d.Workspaces = w.spaces
	d.Awards = w.rewards
	d.Lab = w.state
	d.Terminals = w.terminals
}
