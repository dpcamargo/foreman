package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/dpcamargo/foreman/internal/adapters/container"
	"github.com/dpcamargo/foreman/internal/adapters/sandbox"
	"github.com/dpcamargo/foreman/internal/adapters/sqlite"
	"github.com/dpcamargo/foreman/internal/core/eval"
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
  help       show this help

Run "fm eval <subcommand> -h" for flags.
`)
}

// app holds the wired adapters for one command.
type app struct {
	env      env
	store    *sqlite.Store
	ws       *sandbox.Manager
	grader   *container.Grader
	redactor *eval.Redactor
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
		env:      e,
		store:    store,
		ws:       ws,
		redactor: redactor,
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
