package main

import (
	"context"
	"fmt"
	"io"
	"net/http"

	"github.com/dpcamargo/foreman/internal/adapters/localapi"
	"github.com/dpcamargo/foreman/internal/adapters/proc"
	"github.com/dpcamargo/foreman/internal/adapters/sqlite"
	"github.com/dpcamargo/foreman/internal/core/engine"
	"github.com/dpcamargo/foreman/internal/core/eval"
)

// serve runs the daemon until ctx is cancelled (by a signal, see run in main.go, or by a
// test): opens the store, sweeps lost runs left behind by a previous crash before accepting
// any new dispatch (FR-010), constructs the Supervisor that later tasks wire dispatch
// through, and starts the local API listener. Telegram and the story-specific local API
// routes are wired in later tasks (T022 onward); this is the foundation they attach to.
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

	// The Supervisor's engine.Config is filled in by later tasks (T024 onward) as each
	// user story wires its own dispatch path (Specifier/Implementer/Verifier/Reviewer,
	// the ApprovalGate hook). Constructing it here, with Store already set, gives every
	// later task one shared place to attach to rather than threading config through main.
	sup := engine.NewSupervisor(engine.Config{Store: store}, 2)
	_ = sup // used once Telegram/local-API dispatch handlers are wired (T024+)

	mux := http.NewServeMux()
	mux.HandleFunc("/v1/ping", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("pong"))
	})

	api, err := localapi.New(e.socketPath(), mux)
	if err != nil {
		fmt.Fprintf(stderr, "foreman: start local API: %v\n", err)
		return exitInfra
	}
	serveErrCh := make(chan error, 1)
	go func() { serveErrCh <- api.Serve() }()

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
