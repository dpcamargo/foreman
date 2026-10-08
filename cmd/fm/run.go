// Command fm's `run` subcommand executes one task through Feature 002's durable
// execution loop: spec -> implement -> verify -> review, with ladder escalation,
// then writes a task report and a Feature 001 task record (FR-021).
package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/dpcamargo/foreman/internal/adapters/container"
	"github.com/dpcamargo/foreman/internal/adapters/proc"
	"github.com/dpcamargo/foreman/internal/adapters/runner/codex"
	"github.com/dpcamargo/foreman/internal/adapters/runner/hermes"
	"github.com/dpcamargo/foreman/internal/core/engine"
	"github.com/dpcamargo/foreman/internal/core/eval"
	"github.com/dpcamargo/foreman/internal/core/task"
	"github.com/dpcamargo/foreman/internal/core/verify"
)

// taskFile is the parsed form of `task.yaml` (contracts/cli.md "Task File Format").
type taskFile struct {
	Version      int      `json:"version"`
	TaskType     string   `json:"task_type"`
	Repo         string   `json:"repo"`
	BaseCommit   string   `json:"base_commit"`
	Request      string   `json:"request"`
	Category     string   `json:"category"`
	VisibleTests string   `json:"visible_tests"`
	HiddenTests  string   `json:"hidden_tests"`
	TestCmd      []string `json:"test_cmd"`
}

// parseTaskYAML parses the minimal task.yaml subset used by fm run, without a YAML
// dependency (constitution: standard library first).
func parseTaskYAML(data []byte) (taskFile, error) {
	var tf taskFile
	scanner := bufio.NewScanner(bytes.NewReader(data))
	var currentKey string
	var multiline strings.Builder
	lineNum := 0

	flushMultiline := func() {
		if currentKey == "request" {
			tf.Request = multiline.String()
		}
		currentKey = ""
		multiline.Reset()
	}

	for scanner.Scan() {
		lineNum++
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}

		if currentKey == "request" && (strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t")) {
			if multiline.Len() > 0 {
				multiline.WriteString("\n")
			}
			multiline.WriteString(trimmed)
			continue
		}
		if currentKey != "" {
			flushMultiline()
		}

		parts := strings.SplitN(trimmed, ":", 2)
		if len(parts) != 2 {
			return tf, fmt.Errorf("line %d: invalid format, expected 'key: value'", lineNum)
		}
		key := strings.TrimSpace(parts[0])
		val := strings.TrimSpace(parts[1])

		switch {
		case val == "" || val == "|" || val == "|-":
			currentKey = key
			continue
		}

		switch key {
		case "version":
			v, err := strconv.Atoi(val)
			if err != nil {
				return tf, fmt.Errorf("line %d: version must be an integer: %w", lineNum, err)
			}
			tf.Version = v
		case "task_type":
			tf.TaskType = strings.Trim(val, `"'`)
		case "repo":
			tf.Repo = strings.Trim(val, `"'`)
		case "base_commit":
			tf.BaseCommit = strings.Trim(val, `"'`)
		case "request":
			tf.Request = strings.Trim(val, `"'`)
		case "category":
			tf.Category = strings.Trim(val, `"'`)
		case "visible_tests":
			tf.VisibleTests = strings.Trim(val, `"'`)
		case "hidden_tests":
			tf.HiddenTests = strings.Trim(val, `"'`)
		case "test_cmd":
			var cmd []string
			if err := json.Unmarshal([]byte(val), &cmd); err != nil {
				return tf, fmt.Errorf("line %d: test_cmd must be a JSON array of strings: %w", lineNum, err)
			}
			tf.TestCmd = cmd
		}
	}
	if currentKey != "" {
		flushMultiline()
	}
	if err := scanner.Err(); err != nil {
		return tf, err
	}
	return tf, nil
}

func validateTaskFile(tf taskFile) error {
	if tf.Version != 1 {
		return fmt.Errorf("invalid task version %d (must be 1)", tf.Version)
	}
	if tf.Repo == "" {
		return errors.New("missing required field: repo")
	}
	if tf.BaseCommit == "" {
		return errors.New("missing required field: base_commit")
	}
	if tf.Request == "" {
		return errors.New("missing required field: request")
	}
	return nil
}

func runRun(args []string, stdout, stderr io.Writer) int {
	usageText := `usage: fm run [options] <task.yaml>

Executes a task through the durable execution loop (Feature 002).

Options:
  --resume          reconcile interrupted runs and resume execution
  --format <fmt>    report output format: md (default) or json
  --json            alias for --format json
  --arm <name>      runner arm to use: codex (default) or hermes
  --max-rung <n>    maximum escalation ladder rung (default: 3)
  --output-dir <d>  directory to save reports (default: $FOREMAN_HOME/state/reports)
`
	for _, a := range args {
		if a == "-h" || a == "--help" || a == "help" {
			fmt.Fprint(stdout, usageText)
			return exitOK
		}
	}

	flags := flag.NewFlagSet("fm run", flag.ContinueOnError)
	flags.SetOutput(stderr)

	resume := flags.Bool("resume", false, "reconcile interrupted runs and resume execution")
	format := flags.String("format", "md", "report output format (md or json)")
	armFlag := flags.String("arm", "codex", "runner arm to use (default: codex)")
	maxRung := flags.Int("max-rung", 3, "maximum escalation ladder rung")
	outputDir := flags.String("output-dir", "", "directory to save reports")
	asJSON := flags.Bool("json", false, "output structured JSON report on stdout")

	flags.Usage = func() { fmt.Fprint(stderr, usageText) }

	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return exitOK
		}
		return exitUsage
	}
	if *asJSON {
		*format = "json"
	}

	ctx := context.Background()
	a, err := openApp(ctx)
	if err != nil {
		fmt.Fprintf(stderr, "fm run: %v\n", err)
		return exitInfra
	}
	defer a.close()

	// 1. Reconciliation sweep: mark attempts whose PID is no longer alive as lost.
	reconciler := engine.Reconciler{
		Store: a.store,
		Alive: func(pid int, startedAt string) bool {
			return proc.Alive(eval.PIDFingerprint{PID: pid, StartedAt: startedAt})
		},
	}
	lost, err := reconciler.Sweep(ctx)
	if err != nil {
		fmt.Fprintf(stderr, "fm run: sweep lost runs: %v\n", err)
		return exitInfra
	}
	if len(lost) > 0 {
		fmt.Fprintf(stdout, "reconciled %d lost run(s)\n", len(lost))
	}

	rest := flags.Args()
	if len(rest) == 0 {
		if *resume {
			fmt.Fprintln(stdout, "reconciled lost runs; no task specified to resume")
			return exitOK
		}
		flags.Usage()
		return exitUsage
	}

	taskFilePath := rest[0]
	b, err := os.ReadFile(taskFilePath)
	if err != nil {
		fmt.Fprintf(stderr, "fm run: read task file: %v\n", err)
		return exitUsage
	}
	tf, err := parseTaskYAML(b)
	if err != nil {
		fmt.Fprintf(stderr, "fm run: parse task file: %v\n", err)
		return exitUsage
	}
	if err := validateTaskFile(tf); err != nil {
		fmt.Fprintf(stderr, "fm run: %v\n", err)
		return exitUsage
	}

	taskDir := filepath.Dir(taskFilePath)
	if abs, err := filepath.Abs(taskDir); err == nil {
		taskDir = abs
	}

	repo := expandPath(tf.Repo)
	if !filepath.IsAbs(repo) && !strings.Contains(repo, "://") && !strings.HasPrefix(repo, "git@") {
		if cand := filepath.Join(taskDir, repo); fileExists(cand) {
			repo = cand
		}
	}

	var visFS fs.FS
	if tf.VisibleTests != "" {
		p := expandPath(tf.VisibleTests)
		if !filepath.IsAbs(p) {
			if cand := filepath.Join(taskDir, p); fileExists(cand) {
				p = cand
			}
		}
		visFS, err = dirFS("visible_tests", p)
		if err != nil {
			fmt.Fprintf(stderr, "fm run: visible tests: %v\n", err)
			return exitUsage
		}
	}

	var hidFS fs.FS
	if tf.HiddenTests != "" {
		p := expandPath(tf.HiddenTests)
		if !filepath.IsAbs(p) {
			if cand := filepath.Join(taskDir, p); fileExists(cand) {
				p = cand
			}
		}
		hidFS, err = dirFS("hidden_tests", p)
		if err != nil {
			fmt.Fprintf(stderr, "fm run: hidden tests: %v\n", err)
			return exitUsage
		}
	}

	// Locate .foreman/project.yaml: beside the task file, or in the target repo.
	var projectYAML []byte
	for _, cand := range []string{
		filepath.Join(taskDir, "project.yaml"),
		filepath.Join(taskDir, ".foreman", "project.yaml"),
		filepath.Join(repo, ".foreman", "project.yaml"),
	} {
		if data, err := os.ReadFile(cand); err == nil {
			projectYAML = data
			break
		}
	}
	if len(projectYAML) == 0 {
		if out, err := gitCatFile(ctx, repo, tf.BaseCommit, ".foreman/project.yaml"); err == nil && len(out) > 0 {
			projectYAML = out
		}
	}
	if len(projectYAML) == 0 {
		fmt.Fprintln(stderr, "fm run: missing project config (.foreman/project.yaml)")
		return exitUsage
	}
	projCfg, err := verify.ParseProjectConfig(projectYAML)
	if err != nil {
		fmt.Fprintf(stderr, "fm run: invalid project config: %v\n", err)
		return exitUsage
	}

	// 2. Wire runners and the engine.
	goroot, err := exec.CommandContext(ctx, "go", "env", "GOROOT").Output()
	if err != nil {
		fmt.Fprintf(stderr, "fm run: go env GOROOT: %v\n", err)
		return exitInfra
	}
	home, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintf(stderr, "fm run: user home dir: %v\n", err)
		return exitInfra
	}

	codexArm := codex.New(codex.Config{
		GoRoot:    strings.TrimSpace(string(goroot)),
		ModCache:  filepath.Join(a.env.Work, "gomodcache"),
		Home:      home,
		Path:      os.Getenv("PATH"),
		CodexHome: os.Getenv("CODEX_HOME"),
		ToolRoots: toolRoots("codex"),
	})
	hermesArm := hermes.NewOneshot(hermes.Config{
		HermesHome: a.env.HermesHome,
		ModCache:   filepath.Join(a.env.Work, "gomodcache"),
	})

	selfFP, _ := proc.Self()

	engineCfg := engine.Config{
		Store:         a.store,
		Specifier:     codexArm,
		Verifier:      container.NewVerifier(a.grader, a.redactor.Secrets()),
		Implementers:  map[string]engine.Implementer{"openai": codexArm, "anthropic": hermesArm},
		Reviewers:     map[string]engine.Reviewer{"openai": codexArm, "anthropic": hermesArm},
		Cloner:        a.ws,
		FileExists:    gitTreeFileExists(ctx, repo, tf.BaseCommit),
		MaxRung:       *maxRung,
		DefaultFamily: "openai",
		PID:           selfFP.PID,
		PIDStartedAt:  selfFP.StartedAt,
	}
	if *armFlag == "hermes" {
		engineCfg.DefaultFamily = "anthropic"
	}
	eng := engine.New(engineCfg)

	tsk := task.Task{
		ID:          task.NewID("tsk"),
		TaskType:    task.TaskTypeCode,
		RepoURL:     repo,
		BaseCommit:  tf.BaseCommit,
		RequestText: tf.Request,
		Arm:         *armFlag,
		Status:      task.StatusPending,
	}

	// 3. Run the durable execution loop.
	report, runErr := eng.RunTask(ctx, engine.RunParams{
		Task:        tsk,
		Visible:     visFS,
		Hidden:      hidFS,
		ProjectYAML: projectYAML,
	})

	// 4. Feature 001 task record (FR-021): written whatever the outcome.
	cat := eval.CategoryFeature
	if tf.Category != "" {
		cat = eval.Category(tf.Category)
	}
	testCmd := tf.TestCmd
	if len(testCmd) == 0 && len(projCfg.Verify) > 0 {
		testCmd = projCfg.Verify[0]
	}
	outcome := eval.OutcomeFailure
	switch report.Status {
	case task.StatusSucceeded:
		outcome = eval.OutcomeVerifiedSuccess
	case task.StatusBlockedHuman:
		outcome = eval.OutcomeBlocked
	}
	rec := eval.Record{
		Source:      eval.SourceFmRun,
		TaskType:    eval.TaskTypeCode,
		Category:    cat,
		Request:     tf.Request,
		RepoURL:     repo,
		BaseCommit:  tf.BaseCommit,
		TestCommand: testCmd,
		TestFormat:  eval.FormatGoTestJSON,
		Live: &eval.LiveOutcome{
			Outcome:        outcome,
			Attempts:       report.Attempts,
			TokensBillable: int64(report.TokensTotal),
			Wall:           report.WallTotal,
		},
	}
	recFiles := eval.RecordFiles{Visible: visFS, Hidden: hidFS}
	if addedRec, err := a.store.AddRecord(ctx, rec, recFiles); err == nil {
		_ = a.store.UpdateTaskRecordID(ctx, tsk.ID, addedRec.ID)
	}

	// 5. Write the task report (markdown and JSON), named rpt_<id>.{md,json} (data-model.md).
	// Redact every free-text field at write time (FR-017): a secret echoed into RequestText,
	// SpecSummary or a ReviewFinding must never reach state/reports/.
	report.Redact = a.redactor.Redact
	md := engine.GenerateMarkdownReport(report)
	js, _ := engine.GenerateJSONReport(report)
	rptsDir := filepath.Join(a.env.State, "reports")
	if report.TaskID != "" {
		rptID := task.NewID("rpt")
		_ = os.MkdirAll(rptsDir, 0o700)
		_ = os.WriteFile(filepath.Join(rptsDir, rptID+".md"), []byte(md), 0o600)
		_ = os.WriteFile(filepath.Join(rptsDir, rptID+".json"), js, 0o600)
		if *outputDir != "" {
			_ = os.MkdirAll(*outputDir, 0o755)
			_ = os.WriteFile(filepath.Join(*outputDir, rptID+".md"), []byte(md), 0o644)
			_ = os.WriteFile(filepath.Join(*outputDir, rptID+".json"), js, 0o644)
		}
	}

	if *format == "json" {
		stdout.Write(append(js, '\n'))
	} else {
		fmt.Fprintln(stdout, md)
	}

	if runErr != nil {
		fmt.Fprintf(stderr, "fm run: execution error: %v\n", runErr)
		return exitNegative
	}
	if report.Status == task.StatusSucceeded {
		return exitOK
	}
	return exitNegative
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// expandPath resolves a leading "~/" against the user's home directory.
func expandPath(p string) string {
	if strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, p[2:])
		}
	}
	return p
}

func gitCatFile(ctx context.Context, repo, commit, path string) ([]byte, error) {
	var cmd *exec.Cmd
	if strings.HasSuffix(repo, ".git") || fileExists(filepath.Join(repo, "HEAD")) {
		cmd = exec.CommandContext(ctx, "git", "--git-dir="+repo, "cat-file", "-p", commit+":"+path)
	} else {
		cmd = exec.CommandContext(ctx, "git", "-C", repo, "cat-file", "-p", commit+":"+path)
	}
	return cmd.Output()
}

// gitTreeFileExists checks the target repo's tree at the pinned base commit (spec FR-004):
// cited files must exist in the base commit's git tree, not just in some local checkout.
func gitTreeFileExists(ctx context.Context, repo, commit string) func(string) bool {
	return func(path string) bool {
		var cmd *exec.Cmd
		if strings.HasSuffix(repo, ".git") || fileExists(filepath.Join(repo, "HEAD")) {
			cmd = exec.CommandContext(ctx, "git", "--git-dir="+repo, "cat-file", "-e", commit+":"+path)
		} else {
			cmd = exec.CommandContext(ctx, "git", "-C", repo, "cat-file", "-e", commit+":"+path)
		}
		return cmd.Run() == nil
	}
}
