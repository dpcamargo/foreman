package main

import (
	"fmt"
	"io"
)

// runEval dispatches `fm eval <subcommand>`. Subcommands are added by later tasks
// (specs/001-evaluation-harness/tasks.md).
func runEval(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		evalUsage(stderr)
		return exitUsage
	}
	switch args[0] {
	case "help", "-h", "--help":
		evalUsage(stdout)
		return exitOK
	default:
		fmt.Fprintf(stderr, "fm eval: unknown eval subcommand %q\n\n", args[0])
		evalUsage(stderr)
		return exitUsage
	}
}

func evalUsage(w io.Writer) {
	fmt.Fprint(w, `usage: fm eval <subcommand> [flags]

subcommands:
  help      show this help
`)
}
