// Command foreman is the long-lived daemon: Feature 002's engine plus a Telegram control
// plane and a local unix-socket API (specs/003-daemon-telegram-control). Wiring only — no
// business logic lives here (AGENTS.md: "cmd/ only wires").
package main

import (
	"fmt"
	"io"
	"os"
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
		return serve(stdout, stderr)
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

// serve is wired in later tasks (T019 onward): opens the store, runs the startup
// reconciler, starts the Supervisor, the Telegram client, and the local API listener.
func serve(stdout, stderr io.Writer) int {
	fmt.Fprintln(stderr, "foreman: serve is not wired yet")
	return exitInfra
}

func usage(w io.Writer) {
	fmt.Fprint(w, `usage: foreman [command]

With no command, runs the daemon: Feature 002's engine, dispatched from Telegram and the
local unix-socket API, with a startup reconciler and an outbox notifier.

commands:
  help    show this help
`)
}
