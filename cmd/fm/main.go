// Command fm is foreman's command-line interface. In Feature 001 it only has the
// `eval` subcommand: the task ledger and baseline replays (specs/001-evaluation-harness).
package main

import (
	"fmt"
	"io"
	"os"
)

// Exit codes (specs/001-evaluation-harness/contracts/cli.md).
const (
	exitOK       = 0 // success
	exitNegative = 1 // the command ran but its result is negative
	exitUsage    = 2 // usage or input error
	exitInfra    = 3 // infrastructure error (Docker, git, arm CLI)
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		usage(stderr)
		return exitUsage
	}
	switch args[0] {
	case "help", "-h", "--help":
		usage(stdout)
		return exitOK
	case "eval":
		return runEval(args[1:], stdout, stderr)
	case "run":
		return runRun(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "fm: unknown command %q\n\n", args[0])
		usage(stderr)
		return exitUsage
	}
}

func usage(w io.Writer) {
	fmt.Fprint(w, `usage: fm <command> [arguments]

commands:
  eval    record tasks and replay them through single-agent baselines
  run     execute a task through the durable execution loop
  help    show this help

Run "fm eval help" for the eval subcommands.
`)
}
