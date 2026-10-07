package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/dpcamargo/foreman/internal/adapters/artifacts"
	"github.com/dpcamargo/foreman/internal/adapters/container"
	"github.com/dpcamargo/foreman/internal/adapters/proc"
	"github.com/dpcamargo/foreman/internal/adapters/runner/codex"
	"github.com/dpcamargo/foreman/internal/adapters/runner/hermes"
	"github.com/dpcamargo/foreman/internal/adapters/sandbox"
	"github.com/dpcamargo/foreman/internal/adapters/sqlite"
	"github.com/dpcamargo/foreman/internal/core/eval"
	"github.com/dpcamargo/foreman/prompts"
)

// runEval dispatches `fm eval <subcommand>` (contracts/cli.md).
func runEval(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		evalUsage(stderr)
		return exitUsage
	}
	ctx := context.Background()
	switch args[0] {
	case "help", "-h", "--help":
		evalUsage(stdout)
		return exitOK
	case "record":
		return cmdRecord(ctx, args[1:], stdout, stderr)
	case "validate":
		return cmdValidate(ctx, args[1:], stdout, stderr)
	case "list":
		return cmdList(ctx, args[1:], stdout, stderr)
	case "replay":
		return cmdReplay(ctx, args[1:], stdout, stderr)
	case "spot":
		return cmdSpot(ctx, args[1:], stdout, stderr)
	case "decide":
		return cmdDecide(ctx, args[1:], stdout, stderr)
	case "report":
		return cmdReport(ctx, args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "fm eval: unknown eval subcommand %q\n\n", args[0])
		evalUsage(stderr)
		return exitUsage
	}
}

func evalUsage(w io.Writer) {
	fmt.Fprint(w, `usage: fm eval <subcommand> [flags]

subcommands:
  record     record a task (done outside foreman) as a task record
  validate   check whether records can be replayed
  list       list task records
  replay     replay one record through a baseline arm
  spot       spot check: recent records once through one arm (a trend, not a decision)
  decide     decision replay: seeded sample, smoke pass, budget check, k repetitions
  report     re-render a stored report
  help       show this help

Run "fm eval <subcommand> -h" for flags.
`)
}

// app holds the wired adapters for one command.
type app struct {
	env       env
	store     *sqlite.Store
	ws        *sandbox.Manager
	grader    *container.Grader
	artifacts *artifacts.Store
	redactor  *eval.Redactor
}

func openApp(ctx context.Context) (*app, error) {
	e, err := loadEnv(os.Getenv, os.UserHomeDir)
	if err != nil {
		return nil, err
	}
	if err := e.ensureDirs(); err != nil {
		return nil, err
	}
	store, err := sqlite.Open(ctx, e.State)
	if err != nil {
		return nil, err
	}
	redactor := eval.NewRedactor(environ())
	ws := sandbox.New(e.Work)
	return &app{
		env:       e,
		store:     store,
		ws:        ws,
		artifacts: artifacts.New(e.State, e.Work, redactor.Redact),
		redactor:  redactor,
		grader: container.New(container.Config{
			Image: e.GraderImage, DockerContext: e.DockerContext, Work: e.Work,
			Clones: ws, Redact: redactor.Redact,
		}),
	}, nil
}

func (a *app) close() { a.store.Close() }

func (a *app) validator() eval.Validator { return eval.Validator{Store: a.store, Grader: a.grader} }

func environ() map[string]string {
	m := map[string]string{}
	for _, kv := range os.Environ() {
		if k, v, ok := strings.Cut(kv, "="); ok {
			m[k] = v
		}
	}
	return m
}

// exitFor maps an error to an exit code: input errors are 2, everything else 3.
func exitFor(err error) int {
	if errors.Is(err, eval.ErrInvalidRecord) || errors.Is(err, errUsage) || errors.Is(err, eval.ErrNotFound) {
		return exitUsage
	}
	return exitInfra
}

var errUsage = errors.New("usage error")

func usageErr(format string, a ...any) error {
	return fmt.Errorf("%w: %s", errUsage, fmt.Sprintf(format, a...))
}

// --- fm eval record ---

type recordFlags struct {
	repo, base, requestFile, hidden, visible, reference string
	testCmd, fullTestCmd, testFormat, category          string
	expectsClarification, noValidate, json              bool
	wallBudget                                          time.Duration
	liveOutcome                                         string
	liveAttempts, liveInterventions                     int
	liveTokens                                          int64
	liveUSD                                             float64
	liveWall                                            time.Duration
}

func cmdRecord(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	var f recordFlags
	fs := flag.NewFlagSet("fm eval record", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&f.repo, "repo", "", "repository URL or local path (required)")
	fs.StringVar(&f.base, "base", "", "base commit SHA, 40 hex characters (required)")
	fs.StringVar(&f.requestFile, "request-file", "", "file holding the task request (required)")
	fs.StringVar(&f.hidden, "hidden", "", "directory of hidden tests (goes with --test-cmd)")
	fs.StringVar(&f.testCmd, "test-cmd", "", `test command as a JSON array, e.g. '["go","test","-json","./..."]'`)
	fs.StringVar(&f.testFormat, "test-format", "", "go-test-json (default) or exit-code")
	fs.StringVar(&f.visible, "visible", "", "directory of visible tests copied into the arm's workspace")
	fs.StringVar(&f.reference, "reference", "", "reference patch (the verified result)")
	fs.StringVar(&f.fullTestCmd, "full-test-cmd", "", "full test suite command as a JSON array (regression check)")
	fs.StringVar(&f.category, "category", string(eval.CategoryFeature), "bug_fix|feature|refactor|dependency|research|ambiguous")
	fs.BoolVar(&f.expectsClarification, "expects-clarification", false, "the right outcome is to stop and ask")
	fs.DurationVar(&f.wallBudget, "wall-budget", 30*time.Minute, "wall-clock budget for each replay")
	fs.StringVar(&f.liveOutcome, "live-outcome", "", "verified_success|failure|blocked|rescued|clarification")
	fs.IntVar(&f.liveAttempts, "live-attempts", 0, "attempts in the live run")
	fs.Int64Var(&f.liveTokens, "live-tokens", 0, "billable tokens in the live run")
	fs.Float64Var(&f.liveUSD, "live-usd", 0, "dollars in the live run")
	fs.DurationVar(&f.liveWall, "live-wall", 0, "wall-clock time of the live run")
	fs.IntVar(&f.liveInterventions, "live-interventions", 0, "human rescues in the live run")
	fs.BoolVar(&f.noValidate, "no-validate", false, "store without validating")
	fs.BoolVar(&f.json, "json", false, "print JSON")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	r, files, err := f.build()
	if err != nil {
		fmt.Fprintf(stderr, "fm eval record: %v\n", err)
		return exitFor(err)
	}
	r, err = eval.ValidateNew(r, files)
	if err != nil {
		fmt.Fprintf(stderr, "fm eval record: %v\n", err)
		return exitUsage
	}

	a, err := openApp(ctx)
	if err != nil {
		fmt.Fprintf(stderr, "fm eval record: %v\n", err)
		return exitInfra
	}
	defer a.close()
	added, err := a.store.AddRecord(ctx, r, files)
	if err != nil {
		fmt.Fprintf(stderr, "fm eval record: %v\n", err)
		return exitInfra
	}
	if f.noValidate {
		return printResults(stdout, stderr, f.json, []eval.ValidationResult{{ID: added.ID}}, true)
	}
	if err := a.grader.EnsureImage(ctx); err != nil {
		fmt.Fprintf(stderr, "fm eval record: stored %s, but can't validate: %v\n", added.ID, err)
		return exitInfra
	}
	res, err := a.validator().Validate(ctx, []string{added.ID}, eval.ValidateIDs)
	if err != nil {
		fmt.Fprintf(stderr, "fm eval record: %v\n", err)
		return exitInfra
	}
	return printResults(stdout, stderr, f.json, res, false)
}

func (f recordFlags) build() (eval.Record, eval.RecordFiles, error) {
	if f.repo == "" || f.base == "" || f.requestFile == "" {
		return eval.Record{}, eval.RecordFiles{}, usageErr("--repo, --base and --request-file are required")
	}
	if (f.hidden == "") != (f.testCmd == "") {
		return eval.Record{}, eval.RecordFiles{}, usageErr("--hidden and --test-cmd go together")
	}
	request, err := os.ReadFile(f.requestFile)
	if err != nil {
		return eval.Record{}, eval.RecordFiles{}, usageErr("read --request-file: %v", err)
	}
	r := eval.Record{
		Source: eval.SourceManual, Category: eval.Category(f.category), Request: string(request),
		RepoURL: repoURL(f.repo), BaseCommit: f.base, TestFormat: eval.TestFormat(f.testFormat),
		WallBudget: f.wallBudget, ExpectsClarification: f.expectsClarification,
	}
	if r.TestCommand, err = parseArgvFlag("--test-cmd", f.testCmd); err != nil {
		return eval.Record{}, eval.RecordFiles{}, err
	}
	if r.FullTestCommand, err = parseArgvFlag("--full-test-cmd", f.fullTestCmd); err != nil {
		return eval.Record{}, eval.RecordFiles{}, err
	}
	if f.liveOutcome != "" {
		r.Live = &eval.LiveOutcome{
			Outcome: eval.Outcome(f.liveOutcome), Attempts: f.liveAttempts, TokensBillable: f.liveTokens,
			USD: f.liveUSD, Wall: f.liveWall, Interventions: f.liveInterventions,
		}
	}
	var files eval.RecordFiles
	if files.Hidden, err = dirFS("--hidden", f.hidden); err != nil {
		return eval.Record{}, eval.RecordFiles{}, err
	}
	if files.Visible, err = dirFS("--visible", f.visible); err != nil {
		return eval.Record{}, eval.RecordFiles{}, err
	}
	if f.reference != "" {
		if files.ReferencePatch, err = os.ReadFile(f.reference); err != nil {
			return eval.Record{}, eval.RecordFiles{}, usageErr("read --reference: %v", err)
		}
	}
	return r, files, nil
}

// repoURL makes an existing local path absolute, so the mirror key is stable.
func repoURL(s string) string {
	if strings.Contains(s, "://") || strings.HasPrefix(s, "git@") {
		return s
	}
	if _, err := os.Stat(s); err == nil {
		if abs, err := filepath.Abs(s); err == nil {
			return abs
		}
	}
	return s
}

func parseArgvFlag(name, s string) ([]string, error) {
	if s == "" {
		return nil, nil
	}
	var argv []string
	if err := json.Unmarshal([]byte(s), &argv); err != nil || len(argv) == 0 {
		return nil, usageErr("%s must be a non-empty JSON array of strings, got %q", name, s)
	}
	return argv, nil
}

func dirFS(name, dir string) (fs.FS, error) {
	if dir == "" {
		return nil, nil
	}
	fi, err := os.Stat(dir)
	if err != nil || !fi.IsDir() {
		return nil, usageErr("%s %q is not a directory", name, dir)
	}
	return os.DirFS(dir), nil
}

// printResults prints validation results and returns the exit code: 3 if any record
// hit an infrastructure error, 1 if any is not replayable, else 0.
func printResults(stdout, stderr io.Writer, asJSON bool, res []eval.ValidationResult, pending bool) int {
	code := exitOK
	type jsonResult struct {
		ID         string `json:"id"`
		Replayable *bool  `json:"replayable"`
		Reason     string `json:"reason,omitempty"`
		Error      string `json:"error,omitempty"`
	}
	var out []jsonResult
	for _, r := range res {
		jr := jsonResult{ID: r.ID}
		switch {
		case pending:
			if !asJSON {
				fmt.Fprintf(stdout, "%s  pending validation\n", r.ID)
			}
		case r.Err != nil:
			jr.Error = r.Err.Error()
			fmt.Fprintf(stderr, "%s  not validated (infrastructure error): %v\n", r.ID, r.Err)
			code = exitInfra
		case r.Validation.Replayable:
			ok := true
			jr.Replayable = &ok
			if !asJSON {
				fmt.Fprintf(stdout, "%s  replayable\n", r.ID)
			}
		default:
			no := false
			jr.Replayable, jr.Reason = &no, r.Validation.Reason
			if !asJSON {
				fmt.Fprintf(stdout, "%s  not replayable: %s\n", r.ID, r.Validation.Reason)
			}
			if code == exitOK {
				code = exitNegative
			}
		}
		out = append(out, jr)
	}
	if asJSON {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		enc.Encode(out)
	}
	return code
}

// --- fm eval validate ---

func cmdValidate(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("fm eval validate", flag.ContinueOnError)
	fs.SetOutput(stderr)
	all := fs.Bool("all", false, "revalidate every record")
	pending := fs.Bool("pending", false, "validate records never validated (default without ids)")
	asJSON := fs.Bool("json", false, "print JSON")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	ids := fs.Args()
	mode := eval.ValidatePending
	switch {
	case *all && (*pending || len(ids) > 0), *pending && len(ids) > 0:
		fmt.Fprintln(stderr, "fm eval validate: give ids, --pending or --all, not a mix")
		return exitUsage
	case *all:
		mode = eval.ValidateAll
	case len(ids) > 0:
		mode = eval.ValidateIDs
	}
	a, err := openApp(ctx)
	if err != nil {
		fmt.Fprintf(stderr, "fm eval validate: %v\n", err)
		return exitInfra
	}
	defer a.close()
	if err := a.grader.EnsureImage(ctx); err != nil {
		fmt.Fprintf(stderr, "fm eval validate: %v\n", err)
		return exitInfra
	}
	res, err := a.validator().Validate(ctx, ids, mode)
	if err != nil {
		fmt.Fprintf(stderr, "fm eval validate: %v\n", err)
		return exitFor(err)
	}
	if len(res) == 0 && !*asJSON {
		fmt.Fprintln(stdout, "nothing to validate")
	}
	return printResults(stdout, stderr, *asJSON, res, false)
}

// --- fm eval list ---

func cmdList(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("fm eval list", flag.ContinueOnError)
	fs.SetOutput(stderr)
	replayable := fs.Bool("replayable", false, "only replayable records")
	repo := fs.String("repo", "", "only records for this repository")
	since := fs.String("since", "", "only records created since: a duration (24h) or a date (2026-10-01)")
	asJSON := fs.Bool("json", false, "print JSON")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	filter := eval.RecordFilter{ReplayableOnly: *replayable, RepoURL: repoURL(*repo)}
	if *repo == "" {
		filter.RepoURL = ""
	}
	if *since != "" {
		t, err := parseSince(*since, time.Now())
		if err != nil {
			fmt.Fprintf(stderr, "fm eval list: %v\n", err)
			return exitUsage
		}
		filter.CreatedSince = t
	}
	a, err := openApp(ctx)
	if err != nil {
		fmt.Fprintf(stderr, "fm eval list: %v\n", err)
		return exitInfra
	}
	defer a.close()
	records, err := a.store.Records(ctx, filter)
	if err != nil {
		fmt.Fprintf(stderr, "fm eval list: %v\n", err)
		return exitInfra
	}
	if *asJSON {
		return printListJSON(stdout, records)
	}
	replayableCount := 0
	for _, r := range records {
		fmt.Fprintf(stdout, "%s  %-10s  %-16s  %-26s  %s  %s\n",
			r.ID, r.Category, liveOutcome(r), replayState(r), r.CreatedAt.Format("2006-01-02 15:04"), shortRepo(r.RepoURL))
		if r.Replayable != nil && *r.Replayable {
			replayableCount++
		}
	}
	fmt.Fprintf(stdout, "%d records, %d replayable\n", len(records), replayableCount)
	if since, err := a.evaluator(eval.Replayer{Records: a.store, Runs: a.store}).RecordsSinceLastSpot(ctx); err == nil {
		fmt.Fprintf(stdout, "%d replayable records since the last spot check\n", since)
	}
	return exitOK
}

func printListJSON(w io.Writer, records []eval.Record) int {
	type row struct {
		ID         string    `json:"id"`
		Category   string    `json:"category"`
		Repo       string    `json:"repo"`
		Base       string    `json:"base"`
		Live       string    `json:"live_outcome,omitempty"`
		Replayable *bool     `json:"replayable"`
		Reason     string    `json:"reason,omitempty"`
		CreatedAt  time.Time `json:"created_at"`
	}
	rows := make([]row, 0, len(records))
	for _, r := range records {
		live := ""
		if r.Live != nil {
			live = string(r.Live.Outcome)
		}
		rows = append(rows, row{r.ID, string(r.Category), r.RepoURL, r.BaseCommit, live, r.Replayable, r.NotReplayableReason, r.CreatedAt})
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(rows); err != nil {
		return exitInfra
	}
	return exitOK
}

func parseSince(s string, now time.Time) (time.Time, error) {
	if d, err := time.ParseDuration(s); err == nil {
		return now.Add(-d), nil
	}
	if t, err := time.Parse("2006-01-02", s); err == nil {
		return t, nil
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	return time.Time{}, fmt.Errorf("--since %q is not a duration or a date", s)
}

func liveOutcome(r eval.Record) string {
	if r.Live == nil {
		return "-"
	}
	return string(r.Live.Outcome)
}

func replayState(r eval.Record) string {
	switch {
	case r.Replayable == nil:
		return "pending"
	case *r.Replayable:
		return "replayable"
	default:
		return "no: " + r.NotReplayableReason
	}
}

func shortRepo(u string) string {
	if len(u) <= 48 {
		return u
	}
	return "…" + u[len(u)-47:]
}

// --- fm eval replay ---

// defaultTokenCap bounds one arm run in billable tokens (research R8).
const defaultTokenCap = 1_000_000

func cmdReplay(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("fm eval replay", flag.ContinueOnError)
	fs.SetOutput(stderr)
	recordID := fs.String("record", "", "record id (required)")
	armFlag := fs.String("arm", "a1", "arm: a0, a1 or a2 (optionally with an instance suffix, e.g. a1#2)")
	k := fs.Int("k", 1, "repetitions")
	maxTokens := fs.Int64("max-tokens", defaultTokenCap, "token cap per run (billable tokens)")
	noCache := fs.Bool("no-cache", false, "always run, and never let this run be reused (validation only)")
	asJSON := fs.Bool("json", false, "print JSON")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	if *recordID == "" || *k < 1 || *maxTokens < 1 {
		fmt.Fprintln(stderr, "fm eval replay: --record is required, and --k and --max-tokens must be positive")
		return exitUsage
	}
	a, err := openApp(ctx)
	if err != nil {
		fmt.Fprintf(stderr, "fm eval replay: %v\n", err)
		return exitInfra
	}
	defer a.close()
	arm, err := a.arm(ctx, *armFlag)
	if err != nil {
		fmt.Fprintf(stderr, "fm eval replay: %v\n", err)
		return exitFor(err)
	}
	rec, err := a.store.Record(ctx, *recordID)
	if err != nil {
		fmt.Fprintf(stderr, "fm eval replay: %v\n", err)
		return exitFor(err)
	}
	if rec.Replayable == nil || !*rec.Replayable {
		fmt.Fprintf(stderr, "fm eval replay: %s is not replayable (%s)\n", rec.ID, replayState(rec))
		return exitNegative
	}
	replayer, err := a.replayer(ctx, *maxTokens)
	if err != nil {
		fmt.Fprintf(stderr, "fm eval replay: %v\n", err)
		return exitInfra
	}

	var results []eval.ReplayResult
	code := exitOK
	for rep := 1; rep <= *k; rep++ {
		req := eval.ReplayRequest{Record: rec, Arm: arm, ArmName: *armFlag, Rep: rep}
		if *noCache {
			req.NoCacheNonce = nonce()
		}
		res, err := replayer.ReplayOnce(ctx, req)
		if err != nil {
			fmt.Fprintf(stderr, "fm eval replay: rep %d: %v\n", rep, err)
			return exitFor(err)
		}
		results = append(results, res)
		if res.Run.Status == eval.StatusInfraError {
			code = exitInfra
		}
		if !*asJSON {
			fmt.Fprintln(stdout, formatRun(res))
		}
	}
	spent := spend(results)
	if *asJSON {
		printReplayJSON(stdout, results, spent)
	} else {
		fmt.Fprintln(stdout, spent.String())
	}
	return code
}

// arm returns the adapter for an arm instance name.
func (a *app) arm(ctx context.Context, name string) (eval.Arm, error) {
	base, err := eval.ParseArm(name)
	if err != nil {
		return nil, usageErr("%v", err)
	}
	switch base {
	case "a1":
		goroot, err := exec.CommandContext(ctx, "go", "env", "GOROOT").Output()
		if err != nil {
			return nil, fmt.Errorf("go env GOROOT: %w", err)
		}
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		return codex.New(codex.Config{
			GoRoot: strings.TrimSpace(string(goroot)), ModCache: filepath.Join(a.env.Work, "gomodcache"),
			Home: home, Path: os.Getenv("PATH"), CodexHome: os.Getenv("CODEX_HOME"),
			ToolRoots: toolRoots("codex"),
		}), nil
	case "a0", "a2":
		cfg := hermes.Config{HermesHome: a.env.HermesHome, ModCache: filepath.Join(a.env.Work, "gomodcache")}
		if err := hermes.VerifyProfile(ctx, cfg); err != nil {
			return nil, err
		}
		if base == "a0" {
			return hermes.NewOneshot(cfg), nil
		}
		return hermes.NewKanbanGoal(cfg), nil
	default:
		return nil, usageErr("arm %s is not available yet", base)
	}
}

// replayer wires a Replayer and sweeps runs lost by dead processes first.
func (a *app) replayer(ctx context.Context, tokenCap int64) (eval.Replayer, error) {
	self, err := proc.Self()
	if err != nil {
		return eval.Replayer{}, err
	}
	lost, err := a.store.SweepLostRuns(ctx, proc.Alive)
	if err != nil {
		return eval.Replayer{}, err
	}
	for _, id := range lost {
		a.ws.Destroy(ctx, eval.Workspace{ID: id}) // its workspace, trusted git dir and io dir
	}
	if len(lost) > 0 {
		fmt.Fprintf(os.Stderr, "marked %d run(s) lost by a dead process as infra_error\n", len(lost))
	}
	if err := a.grader.EnsureImage(ctx); err != nil {
		return eval.Replayer{}, err
	}
	return eval.Replayer{
		Records: a.store, Runs: a.store, Workspaces: a.ws, Artifacts: a.artifacts, Grader: a.grader,
		PromptTemplate: prompts.ArmBaseline, StateDir: a.env.State, PID: self, TokenCap: tokenCap,
	}, nil
}

// toolRoots are software install roots an arm may read: the Homebrew prefix and the
// real install directory of the arm's CLI. They hold programs, not secrets.
func toolRoots(binary string) []string {
	var roots []string
	for _, p := range []string{"/opt/homebrew", "/usr/local/Cellar", "/usr/local/Caskroom"} {
		if fi, err := os.Stat(p); err == nil && fi.IsDir() {
			roots = append(roots, p)
		}
	}
	if path, err := exec.LookPath(binary); err == nil {
		if real, err := filepath.EvalSymlinks(path); err == nil {
			dir := filepath.Dir(filepath.Dir(real))
			covered := false
			for _, r := range roots {
				if dir == r || strings.HasPrefix(dir, r+string(filepath.Separator)) {
					covered = true
				}
			}
			if !covered {
				roots = append(roots, dir)
			}
		}
	}
	return roots
}

func nonce() string {
	b := make([]byte, 4)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func formatRun(r eval.ReplayResult) string {
	hidden := "-"
	if r.Grade != nil {
		hidden = fmt.Sprintf("%d/%d", r.Grade.HiddenPassed, r.Grade.HiddenPassed+r.Grade.HiddenFailed)
	}
	cached := ""
	if r.Reused {
		cached = "  (cached)"
	}
	line := fmt.Sprintf("%s  %-6s rep %d  %-15s claimed=%-19s hidden %-5s tokens %-8d wall %s%s",
		r.Run.ID, r.Run.Key.Arm, r.Run.Key.Rep, r.Run.Status, r.Run.Claimed, hidden,
		r.Run.Usage.Billable(), r.Run.Wall.Round(time.Second), cached)
	if r.Run.Error != "" {
		line += "\n    error: " + r.Run.Error
	}
	return line
}

// spendTotal is what a command itself consumed; cache hits cost nothing (FR-016).
type spendTotal struct {
	Tokens    int64         `json:"tokens"`
	USD       *float64      `json:"usd"`
	Wall      time.Duration `json:"wall_ns"`
	Runs      int           `json:"runs"`
	CacheHits int           `json:"cache_hits"`
}

func spend(results []eval.ReplayResult) spendTotal {
	var t spendTotal
	var u eval.Usage
	for _, r := range results {
		if r.Reused {
			t.CacheHits++
			continue
		}
		t.Runs++
		u = u.Add(r.Run.Usage)
		t.Wall += r.Run.Wall
	}
	t.Tokens, t.USD = u.Billable(), u.USD
	return t
}

func (t spendTotal) String() string {
	usd := "n/a"
	if t.USD != nil {
		usd = fmt.Sprintf("$%.2f", *t.USD)
	}
	return fmt.Sprintf("spent: %d billable tokens, %s, %s wall over %d run(s); %d cache hit(s)",
		t.Tokens, usd, t.Wall.Round(time.Second), t.Runs, t.CacheHits)
}

func printReplayJSON(w io.Writer, results []eval.ReplayResult, t spendTotal) {
	type row struct {
		RunID   string         `json:"run_id"`
		Arm     string         `json:"arm"`
		Rep     int            `json:"rep"`
		Status  eval.RunStatus `json:"status"`
		Claimed eval.Claim     `json:"claimed"`
		Grade   *eval.Grade    `json:"grade"`
		Tokens  int64          `json:"tokens_billable"`
		WallS   float64        `json:"wall_s"`
		Cached  bool           `json:"cached"`
		Error   string         `json:"error,omitempty"`
	}
	var rows []row
	for _, r := range results {
		rows = append(rows, row{r.Run.ID, r.Run.Key.Arm, r.Run.Key.Rep, r.Run.Status, r.Run.Claimed, r.Grade,
			r.Run.Usage.Billable(), r.Run.Wall.Seconds(), r.Reused, r.Run.Error})
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.Encode(map[string]any{"runs": rows, "spend": t})
}

// --- fm eval spot / decide / report (US3) ---

func (a *app) evaluator(r eval.Replayer) eval.Evaluator {
	return eval.Evaluator{Replayer: r, Reports: a.store}
}

// writeReport stores the rendered report under state/reports and prints it.
func (a *app) writeReport(stdout, stderr io.Writer, v eval.ReportView, asJSON bool) error {
	dir := filepath.Join(a.env.State, "reports")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	js, err := v.JSON()
	if err != nil {
		return err
	}
	md := v.Markdown()
	if err := os.WriteFile(filepath.Join(dir, v.Report.ID+".json"), js, 0o600); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, v.Report.ID+".md"), []byte(md), 0o600); err != nil {
		return err
	}
	if asJSON {
		stdout.Write(append(js, '\n'))
	} else {
		fmt.Fprint(stdout, md)
	}
	fmt.Fprintf(stderr, "report: %s.{md,json}\n", filepath.Join(dir, v.Report.ID))
	return nil
}

func cmdSpot(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("fm eval spot", flag.ContinueOnError)
	fs.SetOutput(stderr)
	armName := fs.String("arm", "a1", "arm instance")
	records := fs.String("records", "", "comma-separated record ids (default: replayable records since the last spot check)")
	limit := fs.Int("limit", 5, "at most this many records, newest first")
	maxTokens := fs.Int64("max-tokens", defaultTokenCap, "token cap per run (billable tokens)")
	asJSON := fs.Bool("json", false, "print JSON")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	a, err := openApp(ctx)
	if err != nil {
		fmt.Fprintf(stderr, "fm eval spot: %v\n", err)
		return exitInfra
	}
	defer a.close()
	arm, err := a.arm(ctx, *armName)
	if err != nil {
		fmt.Fprintf(stderr, "fm eval spot: %v\n", err)
		return exitFor(err)
	}
	replayer, err := a.replayer(ctx, *maxTokens)
	if err != nil {
		fmt.Fprintf(stderr, "fm eval spot: %v\n", err)
		return exitInfra
	}
	e := a.evaluator(replayer)
	var ids []string
	if *records != "" {
		ids = strings.Split(*records, ",")
	}
	rep, rows, err := e.Spot(ctx, eval.SpotRequest{Arm: arm, ArmName: *armName, RecordIDs: ids, Limit: *limit})
	if err != nil {
		fmt.Fprintf(stderr, "fm eval spot: %v\n", err)
		return exitFor(err)
	}
	if len(rows) == 0 {
		fmt.Fprintln(stdout, "no replayable records since the last spot check")
	}
	since, _ := e.RecordsSinceLastSpot(ctx)
	sample := make([]eval.Record, 0, len(rows))
	for _, r := range rows {
		sample = append(sample, r.Record)
	}
	if err := a.writeReport(stdout, stderr, eval.BuildView(rep, rows, sample, since), *asJSON); err != nil {
		fmt.Fprintf(stderr, "fm eval spot: %v\n", err)
		return exitInfra
	}
	return exitOK
}

func cmdDecide(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("fm eval decide", flag.ContinueOnError)
	fs.SetOutput(stderr)
	armsFlag := fs.String("arms", "", "comma-separated arm instances, e.g. a0,a1,a2 (required)")
	n := fs.Int("n", 15, "records to sample")
	k := fs.Int("k", 3, "repetitions per record and arm")
	seed := fs.Int64("seed", 0, "sampling and bootstrap seed (default: random, and recorded)")
	budgetTokens := fs.Int64("budget-tokens", 0, "refuse if the projected spend exceeds this many billable tokens (required)")
	budgetUSD := fs.Float64("budget-usd", 0, "refuse if the projected dollars exceed this (0 = no dollar budget)")
	usageChecked := fs.Bool("usage-checked", false, "I checked the Anthropic usage page after the smoke pass")
	maxTokens := fs.Int64("max-tokens", defaultTokenCap, "token cap per run (billable tokens)")
	asJSON := fs.Bool("json", false, "print JSON")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	if *armsFlag == "" || *budgetTokens <= 0 || *n < 1 || *k < 1 {
		fmt.Fprintln(stderr, "fm eval decide: --arms and a positive --budget-tokens are required; --n and --k must be positive")
		return exitUsage
	}
	if *seed == 0 {
		*seed = time.Now().UnixNano() % 1_000_000_000
	}
	a, err := openApp(ctx)
	if err != nil {
		fmt.Fprintf(stderr, "fm eval decide: %v\n", err)
		return exitInfra
	}
	defer a.close()
	order := strings.Split(*armsFlag, ",")
	arms := map[string]eval.Arm{}
	for _, name := range order {
		if _, dup := arms[name]; dup {
			fmt.Fprintf(stderr, "fm eval decide: arm %s listed twice; use an instance suffix (e.g. %s#2)\n", name, name)
			return exitUsage
		}
		arm, err := a.arm(ctx, name)
		if err != nil {
			fmt.Fprintf(stderr, "fm eval decide: %v\n", err)
			return exitFor(err)
		}
		arms[name] = arm
	}
	replayer, err := a.replayer(ctx, *maxTokens)
	if err != nil {
		fmt.Fprintf(stderr, "fm eval decide: %v\n", err)
		return exitInfra
	}
	e := a.evaluator(replayer)
	req := eval.DecideRequest{Arms: arms, ArmOrder: order, N: *n, K: *k, Seed: *seed,
		BudgetTokens: *budgetTokens, UsageChecked: *usageChecked}
	if *budgetUSD > 0 {
		req.BudgetUSD = budgetUSD
	}
	fmt.Fprintf(stderr, "decision replay: seed %d, %d records × %d arm(s) × k=%d\n", *seed, *n, len(order), *k)
	res, err := e.Decide(ctx, req)
	if res.Smoke.ID != "" && (err != nil || res.Decision == nil) {
		fmt.Fprintf(stderr, "smoke pass: %s (%d tokens over %d run(s), %d cache hit(s))\n",
			res.Smoke.ID, res.Smoke.Spent.Tokens, res.Smoke.Spent.Runs, res.Smoke.Spent.CacheHits)
	}
	switch {
	case errors.Is(err, eval.ErrNotEnoughRecords), errors.Is(err, eval.ErrUsageCheckNeeded), errors.Is(err, eval.ErrOverBudget):
		fmt.Fprintf(stderr, "fm eval decide: %v\n", err)
		return exitNegative
	case errors.Is(err, eval.ErrSmokeNotClean):
		fmt.Fprintf(stderr, "fm eval decide: %v\n", err)
		return exitInfra
	case err != nil:
		fmt.Fprintf(stderr, "fm eval decide: %v\n", err)
		return exitFor(err)
	}
	since, _ := e.RecordsSinceLastSpot(ctx)
	if err := a.writeReport(stdout, stderr, eval.BuildView(*res.Decision, res.Rows, res.Sample, since), *asJSON); err != nil {
		fmt.Fprintf(stderr, "fm eval decide: %v\n", err)
		return exitInfra
	}
	return exitOK
}

func cmdReport(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("fm eval report", flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "print JSON")
	if err := fs.Parse(args); err != nil || fs.NArg() != 1 {
		fmt.Fprintln(stderr, "usage: fm eval report <rpt-id> [--json]")
		return exitUsage
	}
	a, err := openApp(ctx)
	if err != nil {
		fmt.Fprintf(stderr, "fm eval report: %v\n", err)
		return exitInfra
	}
	defer a.close()
	v, err := a.evaluator(eval.Replayer{Records: a.store, Runs: a.store}).View(ctx, fs.Arg(0))
	if err != nil {
		fmt.Fprintf(stderr, "fm eval report: %v\n", err)
		return exitFor(err)
	}
	if err := a.writeReport(stdout, stderr, v, *asJSON); err != nil {
		fmt.Fprintf(stderr, "fm eval report: %v\n", err)
		return exitInfra
	}
	return exitOK
}
