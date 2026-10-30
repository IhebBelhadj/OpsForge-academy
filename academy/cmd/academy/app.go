package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/IhebBelhadj/opsforge-academy/academy/internal/catalog"
	catalogpg "github.com/IhebBelhadj/opsforge-academy/academy/internal/catalog/postgres"
	"github.com/IhebBelhadj/opsforge-academy/academy/internal/challenge"
	challengepg "github.com/IhebBelhadj/opsforge-academy/academy/internal/challenge/postgres"
	"github.com/IhebBelhadj/opsforge-academy/academy/internal/compiler"
	"github.com/IhebBelhadj/opsforge-academy/academy/internal/config"
	"github.com/IhebBelhadj/opsforge-academy/academy/internal/contentimport"
	"github.com/IhebBelhadj/opsforge-academy/academy/internal/history"
	historypg "github.com/IhebBelhadj/opsforge-academy/academy/internal/history/postgres"
	"github.com/IhebBelhadj/opsforge-academy/academy/internal/httpapi"
	"github.com/IhebBelhadj/opsforge-academy/academy/internal/identity"
	identitypg "github.com/IhebBelhadj/opsforge-academy/academy/internal/identity/postgres"
	"github.com/IhebBelhadj/opsforge-academy/academy/internal/labsession"
	labpg "github.com/IhebBelhadj/opsforge-academy/academy/internal/labsession/postgres"
	"github.com/IhebBelhadj/opsforge-academy/academy/internal/labstate"
	"github.com/IhebBelhadj/opsforge-academy/academy/internal/legacy"
	"github.com/IhebBelhadj/opsforge-academy/academy/internal/legacy/labsdb"
	legacypg "github.com/IhebBelhadj/opsforge-academy/academy/internal/legacy/postgres"
	"github.com/IhebBelhadj/opsforge-academy/academy/internal/operations"
	operationspg "github.com/IhebBelhadj/opsforge-academy/academy/internal/operations/postgres"
	"github.com/IhebBelhadj/opsforge-academy/academy/internal/opsforgeapi"
	"github.com/IhebBelhadj/opsforge-academy/academy/internal/platform"
	"github.com/IhebBelhadj/opsforge-academy/academy/internal/platform/postgres"
	"github.com/IhebBelhadj/opsforge-academy/academy/internal/progress"
	progresspg "github.com/IhebBelhadj/opsforge-academy/academy/internal/progress/postgres"
	"github.com/IhebBelhadj/opsforge-academy/academy/internal/quiz"
	quizpg "github.com/IhebBelhadj/opsforge-academy/academy/internal/quiz/postgres"
	"github.com/IhebBelhadj/opsforge-academy/academy/internal/rewards"
	rewardspg "github.com/IhebBelhadj/opsforge-academy/academy/internal/rewards/postgres"
	"github.com/IhebBelhadj/opsforge-academy/academy/internal/terminal"
	"github.com/IhebBelhadj/opsforge-academy/academy/internal/tutor"
	"github.com/IhebBelhadj/opsforge-academy/academy/internal/workspace"
	workspacepg "github.com/IhebBelhadj/opsforge-academy/academy/internal/workspace/postgres"
	"github.com/IhebBelhadj/opsforge-academy/academy/web"
)

// App is the wired platform: one HTTP server over the services.
type App struct {
	cfg       config.Config
	log       *slog.Logger
	handler   http.Handler
	terminals *terminal.Service
	importer  *importer
	hub       *platform.Hub
	ops       *operations.Service
	labs      *labsession.Service
	ids       *identity.Service
	legacy    *legacy.Service
	progress  *progress.Service
	rewards   *rewards.Service
	tutor     *tutor.Service
	ready     chan string // receives the listen address once serving (tests use port 0)
}

// MigrateLegacy imports Phase 10's labs.db (cfg.LegacyDB) for the local user, then records the courses those
// passes completed and evaluates the badges (the imported rows came without events).
func (a *App) MigrateLegacy(ctx context.Context) (legacy.Report, error) {
	user, err := a.ids.Local(ctx)
	if err != nil {
		return legacy.Report{}, err
	}
	report, err := a.legacy.Migrate(ctx, a.cfg.LegacyDB, user.ID)
	if err != nil {
		return report, err
	}
	if err := a.progress.Refresh(ctx, user.ID); err != nil {
		return report, fmt.Errorf("course completions: %w", err)
	}
	if _, err := a.rewards.Evaluate(ctx, user.ID); err != nil {
		return report, fmt.Errorf("badges: %w", err)
	}
	return report, nil
}

// Run imports the packs, then serves until ctx ends, then stops accepting and gives running requests
// cfg.ShutdownTimeout. A failed import is logged, not fatal: the catalogue keeps the last good import.
func (a *App) Run(ctx context.Context) error {
	// What a stopped process left half done: its operations failed, its attempts in setup made abandonable
	if err := a.ops.Recover(ctx); err != nil {
		return fmt.Errorf("recovering operations: %w", err)
	}
	if n, err := a.labs.Recover(ctx); err != nil {
		return fmt.Errorf("recovering lab attempts: %w", err)
	} else if n > 0 {
		a.log.Warn("lab attempts interrupted during their setup; abandon them to tear them down", slog.Int64("count", n))
	}
	report, err := a.importer.Import(ctx)
	if errors.Is(err, compiler.ErrSteps) {
		// Academy serves its courses without OpsForge; only lab work needs it. Its steps are checked once it answers
		a.log.Warn("OpsForge cannot check the scenario steps; importing them unchecked, labs need OpsForge",
			slog.Any("error", err))
		report, err = a.importer.ImportUnchecked(ctx)
		go a.recheck(ctx)
	}
	if err != nil {
		a.log.Warn("content import failed; serving the last imported catalogue", slog.Any("error", err))
	} else {
		logImport(a.log, report)
	}
	ln, err := (&net.ListenConfig{}).Listen(ctx, "tcp", a.cfg.Addr)
	if err != nil {
		return err
	}
	srv := &http.Server{Handler: a.handler, ReadHeaderTimeout: 10 * time.Second, BaseContext: func(net.Listener) context.Context {
		return context.WithoutCancel(ctx)
	}}
	// Terminal WebSockets are hijacked connections Shutdown does not wait for: hang up their shells, which
	// closes each socket as "going away"
	srv.RegisterOnShutdown(a.terminals.Shutdown)
	// Event streams never go idle either: closing the hub ends them (the pages reconnect to the next server)
	srv.RegisterOnShutdown(a.hub.Close)
	served := make(chan error, 1)
	go func() { served <- srv.Serve(ln) }()
	// The tutor's agents call Academy's MCP server (internal/httpapi/tutor.go) where it listens
	a.tutor.SetEndpoint("http://" + ln.Addr().String() + "/mcp")
	srv.RegisterOnShutdown(a.tutor.Shutdown)
	a.log.Info("serving", slog.String("url", "http://"+ln.Addr().String()+"/"))
	if a.ready != nil {
		a.ready <- ln.Addr().String()
	}

	select {
	case err := <-served:
		return err
	case <-ctx.Done():
	}
	a.log.Info("shutting down", slog.Duration("grace", a.cfg.ShutdownTimeout))
	shutdown, cancel := context.WithTimeout(context.WithoutCancel(ctx), a.cfg.ShutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdown); err != nil {
		return fmt.Errorf("shutdown: %w", err)
	}
	// Lab operations run past their requests: they get what is left of the grace period
	if err := a.ops.Wait(shutdown); err != nil {
		a.log.Warn("lab operations still running at shutdown; the next start marks them interrupted")
	}
	if err := <-served; !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// recheckEvery is how often an unchecked import retries the checked one (tests shorten it).
var recheckEvery = time.Minute

// recheck imports the packs again, steps checked, every recheckEvery until OpsForge answers or ctx ends.
func (a *App) recheck(ctx context.Context) {
	tick := time.NewTicker(recheckEvery)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		report, err := a.importer.Import(ctx)
		if errors.Is(err, compiler.ErrSteps) {
			continue
		}
		if err != nil {
			a.log.Error("checked content import failed; serving the unchecked catalogue", slog.Any("error", err))
		} else {
			a.log.Info("OpsForge checked the scenario steps")
			logImport(a.log, report)
		}
		return
	}
}

func logImport(log *slog.Logger, report contentimport.Report) {
	log.Info("content imported", slog.Bool("changed", report.Changed), slog.Any("added", report.Added),
		slog.Any("updated", report.Updated), slog.Any("retired", report.Retired))
}

// Providers (cmd/academy/wire.go wires them; tests build what they need by hand).

func provideLogger(cfg config.Config) *slog.Logger {
	return platform.NewLogger(os.Stderr, cfg.LogLevel)
}

func provideStore(ctx context.Context, cfg config.Config, clock platform.Clock) (*postgres.Store, func(), error) {
	store, err := postgres.Open(ctx, cfg.Database, clock)
	if err != nil {
		return nil, nil, err
	}
	return store, func() { _ = store.Close() }, nil
}

// provideOpsForge is OpsForge's API with its token (a copy of OpsForge's .opsforge/api-token, read at each call),
// trusting --opsforge-ca-file when OpsForge serves a self-signed certificate.
func provideOpsForge(cfg config.Config) (*opsforgeapi.Client, error) {
	c, err := opsforgeapi.New(cfg.OpsForgeURL, opsforgeapi.TokenFile(cfg.OpsForgeTokenFile), cfg.OpsForgeTimeout,
		cfg.LabTimeout)
	if err == nil && cfg.OpsForgeCAFile != "" {
		err = c.TrustCA(cfg.OpsForgeCAFile)
	}
	return c, err
}

// provideReadiness: the database decides readiness; OpsForge is reported (lab work needs it, the courses do not).
func provideReadiness(store *postgres.Store, opsforge *opsforgeapi.Client) []httpapi.ReadinessCheck {
	return []httpapi.ReadinessCheck{{Name: "database", Probe: store}, {Name: "opsforge", Probe: opsforge, Optional: true}}
}

// provideTerminals runs the learners' shells on the lab host (OpsForge's WebSocket terminal).
func provideTerminals(opsforge *opsforgeapi.Client) (*terminal.Service, func()) {
	svc := terminal.NewService(opsforge.Shells())
	return svc, svc.Shutdown
}

func provideShells(svc *terminal.Service) terminal.Shells { return terminal.NewShells(svc) }

func provideIdentity(store *postgres.Store, clock platform.Clock) *identity.Service {
	return identity.NewService(identitypg.NewRepo(store.DB), clock)
}

func provideHub() (*platform.Hub, func()) {
	hub := platform.NewHub()
	return hub, hub.Close
}

func provideHistory(store *postgres.Store, hub *platform.Hub, clock platform.Clock, log *slog.Logger) *history.Service {
	return history.NewService(historypg.NewRepo(store.DB), hub, clock, log)
}

func provideCatalogRepo(store *postgres.Store) *catalogpg.Repo {
	return catalogpg.NewRepo(store.DB)
}

// provideCompiler compiles the content folder's packs; OpsForge checks their scenario steps.
func provideCompiler(cfg config.Config, opsforge *opsforgeapi.Client, clock platform.Clock) *compiler.Service {
	return compiler.NewService(cfg.Content, opsforge, clock)
}

func provideImporter(packs *compiler.Service, repo *catalogpg.Repo, clock platform.Clock) *contentimport.Service {
	return contentimport.NewService(packs, repo, clock)
}

// importer imports the packs, then evaluates every user's badges: a badge added or changed by the import is
// awarded for what was done before it.
type importer struct {
	content *contentimport.Service
	rewards *rewards.Service
	log     *slog.Logger
}

func provideRewardingImporter(content *contentimport.Service, awards *rewards.Service, log *slog.Logger) *importer {
	return &importer{content: content, rewards: awards, log: log}
}

// Import imports, then awards; a failed evaluation is logged (the import itself happened).
func (i *importer) Import(ctx context.Context) (contentimport.Report, error) {
	return i.award(ctx, i.content.Import)
}

// ImportUnchecked imports without OpsForge's step check, then awards.
func (i *importer) ImportUnchecked(ctx context.Context) (contentimport.Report, error) {
	return i.award(ctx, i.content.ImportUnchecked)
}

func (i *importer) award(ctx context.Context, imp func(context.Context) (contentimport.Report, error)) (
	contentimport.Report, error) {
	report, err := imp(ctx)
	if err != nil {
		return report, err
	}
	if n, err := i.rewards.EvaluateAll(ctx); err != nil {
		i.log.Error("evaluating badges after the import", slog.Any("error", err))
	} else if n > 0 {
		i.log.Info("badges awarded after the import", slog.Int("count", n))
	}
	return report, nil
}

func provideCatalog(repo *catalogpg.Repo) *catalog.Service { return catalog.NewService(repo) }

func provideOperations(store *postgres.Store, hub *platform.Hub, clock platform.Clock, log *slog.Logger) *operations.Service {
	return operations.NewService(operationspg.NewRepo(store.DB), hub, clock, log)
}

// provideWorkspaces keeps the learners' files on the lab host, where their shells run.
func provideWorkspaces(opsforge *opsforgeapi.Client, store *postgres.Store, clock platform.Clock) *workspace.Service {
	return workspace.NewService(opsforge.Files(), workspacepg.NewRepo(store.DB), clock)
}

func provideLabs(store *postgres.Store, courses *catalog.Service, opsforge *opsforgeapi.Client,
	spaces *workspace.Service, ops *operations.Service, events *history.Service, clock platform.Clock) *labsession.Service {
	return labsession.NewService(labpg.NewRepo(store.DB), courses, opsforge.Sandboxes(), spaces, ops, events, clock)
}

// provideChallenges judges code on OpsForge's sandboxes, one per run or submission.
func provideChallenges(store *postgres.Store, courses *catalog.Service, opsforge *opsforgeapi.Client,
	ops *operations.Service, events *history.Service, clock platform.Clock) *challenge.Service {
	return challenge.NewService(challengepg.NewRepo(store.DB), courses, opsforge.Sandboxes(), ops, events, clock)
}

func provideQuizzes(store *postgres.Store, courses *catalog.Service, events *history.Service,
	clock platform.Clock) *quiz.Service {
	return quiz.NewService(quizpg.NewRepo(store.DB), courses, events, platform.CryptoRandom{}, clock)
}

// provideProgress reads the items the other modules complete (passed quiz attempts and labs) and subscribes to the
// history (a completed course is noticed after the event that completed its last item).
func provideProgress(store *postgres.Store, courses *catalog.Service, events *history.Service, clock platform.Clock,
	quizzes *quiz.Service, labs *labsession.Service, challenges *challenge.Service) *progress.Service {
	passedQuizzes := progress.SourceFunc(func(ctx context.Context, user int64) ([]progress.Completion, error) {
		passed, err := quizzes.Passed(ctx, user)
		out := make([]progress.Completion, len(passed))
		for i, a := range passed {
			out[i] = progress.Completion{Type: catalog.ItemQuiz, Ref: a.Course + "/" + a.Item, Hash: a.Hash,
				At: *a.SubmittedAt}
		}
		return out, err
	})
	passedLabs := progress.SourceFunc(func(ctx context.Context, user int64) ([]progress.Completion, error) {
		passed, err := labs.Passed(ctx, user)
		out := make([]progress.Completion, len(passed))
		for i, a := range passed {
			out[i] = progress.Completion{Type: catalog.ItemLab, Ref: a.Lab, Hash: a.Hash, At: *a.FinishedAt}
		}
		return out, err
	})
	accepted := progress.SourceFunc(func(ctx context.Context, user int64) ([]progress.Completion, error) {
		subs, err := challenges.Accepted(ctx, user)
		out := make([]progress.Completion, len(subs))
		for i, s := range subs {
			out[i] = progress.Completion{Type: catalog.ItemChallenge, Ref: s.Course + "/" + s.Item, Hash: s.Hash,
				At: s.SubmittedAt}
		}
		return out, err
	})
	svc := progress.NewService(progresspg.NewRepo(store.DB), courses, events, clock,
		progress.Sources{passedQuizzes, passedLabs, accepted})
	events.Subscribe(svc.OnEvent)
	return svc
}

// provideRewards reads the history for the badge rules from the modules that record it (lab and quiz passes,
// completed courses) and subscribes to the history (an event that may earn a badge evaluates the user's badges).
func provideRewards(store *postgres.Store, courses *catalog.Service, events *history.Service, clock platform.Clock,
	log *slog.Logger, labs *labsession.Service, quizzes *quiz.Service, challenges *challenge.Service,
	learning *progress.Service, ids *identity.Service) *rewards.Service {
	sources := rewards.Sources{
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
		Challenges: func(ctx context.Context, user int64) ([]rewards.ChallengePass, error) {
			subs, err := challenges.Accepted(ctx, user)
			out := make([]rewards.ChallengePass, len(subs))
			for i, s := range subs {
				out[i] = rewards.ChallengePass{Course: s.Course, Item: s.Item, XP: s.XP}
			}
			return out, err
		},
		Courses: learning.CompletedCourses,
		Users:   ids.UserIDs,
	}
	svc := rewards.NewService(rewardspg.NewRepo(store.DB), courses, sources, events, clock, log)
	events.Subscribe(svc.OnEvent)
	return svc
}

// provideLabState caches OpsForge's lab state; a lab event invalidates it.
func provideLabState(opsforge *opsforgeapi.Client, events *history.Service, clock platform.Clock) *labstate.Service {
	svc := labstate.NewService(opsforge, clock)
	events.Subscribe(svc.OnEvent)
	return svc
}

func provideHandler(build platform.Build, checks []httpapi.ReadinessCheck, shells terminal.Shells,
	ids *identity.Service, importer *importer, courses *catalog.Service, events *history.Service,
	hub *platform.Hub, learning *progress.Service, quizzes *quiz.Service, challenges *challenge.Service,
	labs *labsession.Service, ops *operations.Service, spaces *workspace.Service, awards *rewards.Service,
	state *labstate.Service, tutors *tutor.Service, log *slog.Logger) http.Handler {
	return httpapi.NewHandler(httpapi.Deps{
		Build: build, Readiness: checks, Web: web.FS(), Log: log, Terminals: shells, Identity: ids,
		Importer: importer, Catalog: courses, History: events, Streams: hub,
		Progress: learning, Quizzes: quizzes, Challenges: challenges, Labs: labs, Operations: ops, Workspaces: spaces, Awards: awards,
		Lab: state, Tutor: tutors,
	})
}

func provideLegacy(store *postgres.Store) *legacy.Service {
	return legacy.NewService(labsdb.Reader{}, legacypg.NewStore(store.DB))
}

func newApp(cfg config.Config, log *slog.Logger, handler http.Handler, terminals *terminal.Service,
	importer *importer, hub *platform.Hub, ops *operations.Service, labs *labsession.Service,
	ids *identity.Service, old *legacy.Service, learning *progress.Service, awards *rewards.Service,
	tutors *tutor.Service) *App {
	return &App{cfg: cfg, log: log, handler: handler, terminals: terminals, importer: importer, hub: hub, ops: ops,
		labs: labs, ids: ids, legacy: old, progress: learning, rewards: awards, tutor: tutors}
}
