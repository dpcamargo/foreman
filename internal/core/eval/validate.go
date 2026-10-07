package eval

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// ValidateMode selects which records Validate checks.
type ValidateMode int

const (
	ValidateIDs     ValidateMode = iota // exactly the given ids
	ValidatePending                     // records never validated
	ValidateAll                         // every record (e.g. after a grader image bump)
)

// DefaultGradeTimeout bounds one grading run when the Validator sets none.
const DefaultGradeTimeout = 10 * time.Minute

// Not-replayable reasons (data-model.md, "Validation rules"), checked in this order.
const (
	ReasonNoTests          = "no acceptance tests"
	ReasonBaseNotFetchable = "base commit not fetchable"
	ReasonPassesOnBase     = "hidden tests pass on base"
	ReasonReferenceNoApply = "reference patch does not apply"
	ReasonReferenceFails   = "reference patch fails hidden tests"
	reasonNoVerifierPrefix = "no verifier for task type "
)

// ValidationResult reports one record. Err is an infrastructure failure: the record
// was left unvalidated and can be retried.
type ValidationResult struct {
	ID         string
	Validation Validation
	Err        error
}

// Validator decides whether records can be replayed (spec FR-004).
type Validator struct {
	Store   RecordStore
	Grader  Grader
	Timeout time.Duration // per grading run; DefaultGradeTimeout when zero
}

// Validate checks the selected records and stores each verdict. It returns an error
// only when the selection itself fails (for example an unknown id).
func (v Validator) Validate(ctx context.Context, ids []string, mode ValidateMode) ([]ValidationResult, error) {
	records, err := v.selectRecords(ctx, ids, mode)
	if err != nil {
		return nil, err
	}
	results := make([]ValidationResult, 0, len(records))
	for _, r := range records {
		verdict, err := v.check(ctx, r)
		if err == nil {
			err = v.Store.SetReplayable(ctx, r.ID, verdict)
		}
		results = append(results, ValidationResult{ID: r.ID, Validation: verdict, Err: err})
	}
	return results, nil
}

func (v Validator) selectRecords(ctx context.Context, ids []string, mode ValidateMode) ([]Record, error) {
	switch mode {
	case ValidatePending:
		return v.Store.Records(ctx, RecordFilter{PendingOnly: true})
	case ValidateAll:
		return v.Store.Records(ctx, RecordFilter{})
	}
	var out []Record
	for _, id := range ids {
		r, err := v.Store.Record(ctx, id)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, nil
}

// check applies the replayability rules in order and returns the first that fails.
func (v Validator) check(ctx context.Context, r Record) (Validation, error) {
	files, err := v.Store.RecordFiles(ctx, r.ID)
	if err != nil {
		return Validation{}, fmt.Errorf("read files of %s: %w", r.ID, err)
	}
	hidden, err := ListFiles(files.Hidden)
	if err != nil {
		return Validation{}, fmt.Errorf("list hidden tests of %s: %w", r.ID, err)
	}
	if len(hidden) == 0 || len(r.TestCommand) == 0 {
		return notReplayable(ReasonNoTests), nil
	}
	if r.TaskType != TaskTypeCode {
		return notReplayable(reasonNoVerifierPrefix + r.TaskType), nil
	}

	in := GradeInput{
		RepoURL: r.RepoURL, Base: r.BaseCommit,
		Visible: files.Visible, Hidden: files.Hidden,
		TestCommand: r.TestCommand, TestFormat: r.TestFormat,
		FullTest: r.FullTestCommand, Timeout: v.timeout(),
	}
	base, err := v.Grader.Grade(ctx, in)
	if errors.Is(err, ErrBaseNotFound) {
		return notReplayable(ReasonBaseNotFetchable), nil
	}
	if err != nil {
		return Validation{}, fmt.Errorf("grade base of %s: %w", r.ID, err)
	}
	if base.ApplyError != "" {
		return Validation{}, fmt.Errorf("grade base of %s: visible tests did not apply: %s", r.ID, base.ApplyError)
	}
	verdict := Validation{Replayable: true, BaseFullSuitePassed: base.FullSuitePassed}
	if base.HiddenFailed == 0 {
		verdict.Replayable, verdict.Reason = false, ReasonPassesOnBase
		return verdict, nil
	}
	if files.ReferencePatch == nil {
		return verdict, nil
	}

	in.Patch, in.FullTest = files.ReferencePatch, nil
	ref, err := v.Grader.Grade(ctx, in)
	if err != nil {
		return Validation{}, fmt.Errorf("grade reference of %s: %w", r.ID, err)
	}
	switch {
	case ref.ApplyError != "":
		verdict.Replayable, verdict.Reason = false, ReasonReferenceNoApply
	case ref.HiddenFailed > 0 || ref.HiddenPassed == 0:
		verdict.Replayable, verdict.Reason = false, ReasonReferenceFails
	}
	return verdict, nil
}

func (v Validator) timeout() time.Duration {
	if v.Timeout > 0 {
		return v.Timeout
	}
	return DefaultGradeTimeout
}

func notReplayable(reason string) Validation { return Validation{Replayable: false, Reason: reason} }
