package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestRunTopLevel(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		wantCode   int
		wantStdout string
		wantStderr string
	}{
		{name: "no args prints usage to stderr", args: nil, wantCode: exitUsage, wantStderr: "usage: fm"},
		{name: "help prints usage to stdout", args: []string{"help"}, wantCode: exitOK, wantStdout: "usage: fm"},
		{name: "-h is help", args: []string{"-h"}, wantCode: exitOK, wantStdout: "usage: fm"},
		{name: "unknown command", args: []string{"bogus"}, wantCode: exitUsage, wantStderr: `unknown command "bogus"`},
		{name: "eval without subcommand", args: []string{"eval"}, wantCode: exitUsage, wantStderr: "usage: fm eval"},
		{name: "eval help", args: []string{"eval", "help"}, wantCode: exitOK, wantStdout: "usage: fm eval"},
		{name: "eval unknown subcommand", args: []string{"eval", "bogus"}, wantCode: exitUsage, wantStderr: `unknown eval subcommand "bogus"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := run(tt.args, &stdout, &stderr)
			if code != tt.wantCode {
				t.Errorf("exit code = %d, want %d (stderr: %s)", code, tt.wantCode, stderr.String())
			}
			if tt.wantStdout != "" && !strings.Contains(stdout.String(), tt.wantStdout) {
				t.Errorf("stdout = %q, want it to contain %q", stdout.String(), tt.wantStdout)
			}
			if tt.wantStderr != "" && !strings.Contains(stderr.String(), tt.wantStderr) {
				t.Errorf("stderr = %q, want it to contain %q", stderr.String(), tt.wantStderr)
			}
		})
	}
}
