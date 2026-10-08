package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/dpcamargo/foreman/internal/adapters/container"
	"github.com/dpcamargo/foreman/internal/adapters/integrator"
	"github.com/dpcamargo/foreman/internal/adapters/localapi"
	"github.com/dpcamargo/foreman/internal/adapters/proc"
	"github.com/dpcamargo/foreman/internal/adapters/runner/codex"
	"github.com/dpcamargo/foreman/internal/adapters/runner/hermes"
	"github.com/dpcamargo/foreman/internal/adapters/sandbox"
	"github.com/dpcamargo/foreman/internal/adapters/sqlite"
	"github.com/dpcamargo/foreman/internal/adapters/telegram"
	"github.com/dpcamargo/foreman/internal/core/engine"
	"github.com/dpcamargo/foreman/internal/core/eval"
)

// serve runs the daemon until ctx is cancelled (by a signal, see run in main.go, or by a
// test): opens the store, sweeps lost runs left behind by a previous crash before accepting
// any new dispatch (FR-010), wires one shared engine behind the Supervisor, and starts the
// local API listener plus (if TELEGRAM_BOT_TOKEN is set) the Telegram client.
func serve(ctx context.Context, e env, stdout, stderr io.Writer) int {
	if err := e.ensureDirs(); err != nil {
		fmt.Fprintf(stderr, "foreman: %v\n", err)
		return exitInfra
	}

	store, err := sqlite.Open(ctx, e.State)
	if err != nil {
		fmt.Fprintf(stderr, "foreman: open store: %v\n", err)
		return exitInfra
	}
	defer store.Close()

	reconciler := engine.Reconciler{
		Store: store,
		Alive: func(pid int, startedAt string) bool {
			return proc.Alive(eval.PIDFingerprint{PID: pid, StartedAt: startedAt})
		},
	}
	lost, err := reconciler.Sweep(ctx)
	if err != nil {
		fmt.Fprintf(stderr, "foreman: sweep lost runs: %v\n", err)
		return exitInfra
	}
	if len(lost) > 0 {
		fmt.Fprintf(stdout, "reconciled %d lost run(s)\n", len(lost))
	}

	engineCfg, err := buildEngineConfig(ctx, e, store)
	if err != nil {
		fmt.Fprintf(stderr, "foreman: wire engine: %v\n", err)
		return exitInfra
	}

	var telegramClient *telegram.Client
	var sender messageSender // left as a true nil interface if Telegram isn't configured,
	// never a non-nil interface wrapping a nil *telegram.Client (a classic Go footgun that
	// would make requestSpecApproval's "if sender != nil" check pass incorrectly)
	if e.TelegramToken != "" {
		telegramClient = telegram.NewClient(e.TelegramToken)
		sender = telegramClient
	}

	// The integrator (US4: /deliver and /merge) needs FOREMAN_GITHUB_TOKEN and a target
	// owner/repo — read from the same .env the Telegram token comes from. Left nil (and the
	// delivery gate no-op) until both are set.
	var ig pusher
	if e.GitHubToken != "" && e.GitHubOwner != "" && e.GitHubRepo != "" {
		ig = integrator.New(e.Work, e.GitHubOwner, e.GitHubRepo, e.GitHubToken)
	}

	// sup doesn't exist yet — NewSupervisor builds the Engine from engineCfg immediately
	// below — so the ApprovalGate closure captures this variable by reference rather than
	// by value. By the time the closure actually runs (during a later Dispatch call, well
	// after NewSupervisor returns), sup is already assigned (see approvals.go's doc
	// comment on requestSpecApproval).
	var sup *engine.Supervisor
	engineCfg.ApprovalGate = func(ctx context.Context, taskID string, spec engine.TaskSpec) (bool, error) {
		return requestSpecApproval(ctx, store, sup, sender, taskID, spec)
	}
	sup = engine.NewSupervisor(engineCfg, 2)

	// T041's delivery gate: fires after every successful RunTask (see tasks.go's
	// dispatchOnDone). No-op if the integrator isn't configured yet.
	dispatchOnDone = func(ctx context.Context, report engine.TaskReport, err error) {
		if err != nil || ig == nil {
			return
		}
		_ = requestDeliveryApproval(ctx, store, sup, sender, ig, report)
	}
	mergePusher = ig

	mux := newAPIMux(store, sup)
	api, err := localapi.New(e.socketPath(), mux)
	if err != nil {
		fmt.Fprintf(stderr, "foreman: start local API: %v\n", err)
		return exitInfra
	}
	serveErrCh := make(chan error, 1)
	go func() { serveErrCh <- api.Serve() }()

	// T045/T046 housekeeping tick: retention sweep + unanswered-approval ntfy ping
	// (FR-018, spec.md Assumptions' 4h threshold).
	go startHousekeepingTicker(ctx, e, store, ntfyForEnv(e), stdout, time.Hour)

	if telegramClient != nil {
		go pollTelegram(ctx, telegramClient, store, sup)
	} else {
		fmt.Fprintln(stdout, "foreman: TELEGRAM_BOT_TOKEN not set; local API only")
	}

	select {
	case <-ctx.Done():
		_ = api.Close()
		<-serveErrCh // wait for Serve's goroutine to actually return before exiting
		return exitOK
	case err := <-serveErrCh:
		if err != nil {
			fmt.Fprintf(stderr, "foreman: local API server: %v\n", err)
			return exitInfra
		}
		return exitOK
	}
}

// pollTelegram runs the long-poll loop until ctx is cancelled, checking the allowlist and
// chat type before any command executes (FR-003) and dropping anything that fails that
// check with no reply at all.
func pollTelegram(ctx context.Context, client *telegram.Client, store *sqlite.Store, sup *engine.Supervisor) {
	disp := telegram.Dispatcher{Allowlist: allowlistAdapter{store}}
	_ = client.Poll(ctx, 0, func(u telegram.Update) {
		allowed, err := disp.Allowed(ctx, u)
		if err != nil || !allowed {
			return
		}
		handleUpdate(ctx, store, sup, client, u)
	})
}

// allowlistAdapter adapts *sqlite.Store's IsAllowedUser to telegram.AllowlistChecker.
type allowlistAdapter struct{ store *sqlite.Store }

func (a allowlistAdapter) IsAllowed(ctx context.Context, userID int64) (bool, error) {
	return a.store.IsAllowedUser(ctx, userID)
}

// buildEngineConfig wires the one shared engine.Config every dispatched task runs through
// (codex + hermes arms, the container verifier, the sandbox workspace cloner — the same
// adapters cmd/fm/run.go wires for a single task.yaml run).
//
// FileExists is a known simplification: Feature 002's engine.Config has one FileExists
// closure shared by every dispatch, with no per-task repo/commit context passed through it.
// cmd/fm/run.go can close over one task.yaml's repo+commit because it only ever runs one
// task per process; the daemon runs many tasks, for potentially different repos,
// concurrently through one shared Engine. Closing over a single repo+commit here would be
// wrong for every other repo, and there is no per-call hook in Feature 002 to pass one in
// (changing that is out of scope for this feature — see research.md R7 for the one
// engine.Config change this feature does make). Spec citations are therefore not checked
// against the base commit for daemon-dispatched tasks; ValidateTaskSpec's other checks
// (non-empty summary, acceptance criteria, verify commands) still apply.
func buildEngineConfig(ctx context.Context, e env, store *sqlite.Store) (engine.Config, error) {
	goroot, err := exec.CommandContext(ctx, "go", "env", "GOROOT").Output()
	if err != nil {
		return engine.Config{}, fmt.Errorf("go env GOROOT: %w", err)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return engine.Config{}, fmt.Errorf("user home dir: %w", err)
	}

	codexArm := codex.New(codex.Config{
		GoRoot:   strings.TrimSpace(string(goroot)),
		ModCache: filepath.Join(e.Work, "gomodcache"),
		Home:     home,
		Path:     os.Getenv("PATH"),
	})
	hermesArm := hermes.NewOneshot(hermes.Config{
		HermesHome: e.HermesHome,
		ModCache:   filepath.Join(e.Work, "gomodcache"),
	})
	redactor := eval.NewRedactor(environMap())
	ws := sandbox.New(e.Work)
	grader := container.New(container.Config{
		Image: e.GraderImage, DockerContext: e.DockerContext, Work: e.Work,
		Clones: ws, Redact: redactor.Redact,
	})

	selfFP, _ := proc.Self()
	return engine.Config{
		Store:         store,
		Specifier:     codexArm,
		Verifier:      container.NewVerifier(grader, redactor.Secrets()),
		Implementers:  map[string]engine.Implementer{"openai": codexArm, "anthropic": hermesArm},
		Reviewers:     map[string]engine.Reviewer{"openai": codexArm, "anthropic": hermesArm},
		Cloner:        ws,
		FileExists:    func(string) bool { return true }, // see doc comment above
		DefaultFamily: "openai",
		PID:           selfFP.PID,
		PIDStartedAt:  selfFP.StartedAt,
	}, nil
}

func environMap() map[string]string {
	m := map[string]string{}
	for _, kv := range os.Environ() {
		if k, v, ok := strings.Cut(kv, "="); ok {
			m[k] = v
		}
	}
	return m
}
