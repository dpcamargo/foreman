// Command foreman is the long-lived daemon: Feature 002's engine plus a Telegram control
// plane and a local unix-socket API (specs/003-daemon-telegram-control). Wiring only — no
// business logic lives here (AGENTS.md: "cmd/ only wires").
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
)

// Exit codes, same convention as cmd/fm (specs/001-evaluation-harness/contracts/cli.md).
const (
	exitOK       = 0 // success
	exitNegative = 1 // the command ran but its result is negative
	exitUsage    = 2 // usage or input error
	exitInfra    = 3 // infrastructure error (Docker, git, Telegram, GitHub)
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		e, err := loadEnv(os.Getenv, os.UserHomeDir)
		if err != nil {
			fmt.Fprintf(stderr, "foreman: %v\n", err)
			return exitUsage
		}
		// SIGTERM/SIGINT stop accepting new dispatch and exit cleanly, so launchd's
		// KeepAlive restarts into a startup reconciler that finds a consistent state
		// (deploy/launchd/com.foreman.plist, FR-010).
		ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer cancel()
		return serve(ctx, e, stdout, stderr)
	}
	switch args[0] {
	case "help", "-h", "--help":
		usage(stdout)
		return exitOK
	default:
		fmt.Fprintf(stderr, "foreman: unknown command %q\n\n", args[0])
		usage(stderr)
		return exitUsage
	}
}

func usage(w io.Writer) {
	fmt.Fprint(w, `usage: foreman [command]

With no command, runs the daemon: Feature 002's engine, dispatched from Telegram and the
local unix-socket API, with a startup reconciler and an outbox notifier.

commands:
  help    show this help
`)
}
