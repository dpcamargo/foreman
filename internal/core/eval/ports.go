package eval

import "context"

// Ports: the interfaces the eval use cases need, declared where they are used
// (constitution X; contracts/ports.md). Each has one current adapter.

// Validation is the outcome of checking whether a record can be replayed.
type Validation struct {
	Replayable          bool
	Reason              string // why not, when Replayable is false
	BaseFullSuitePassed *bool  // set when the record has a full test command
}

// RecordStore persists records. Adapter: internal/adapters/sqlite. Every method that
// changes state writes its events row in the same transaction (constitution VII).
type RecordStore interface {
	// AddRecord assigns an id, stores the record and copies its files.
	AddRecord(ctx context.Context, r Record, f RecordFiles) (Record, error)
	Record(ctx context.Context, id string) (Record, error)
	Records(ctx context.Context, f RecordFilter) ([]Record, error)
	// RecordFiles returns a record's stored blobs (read-only views).
	RecordFiles(ctx context.Context, id string) (RecordFiles, error)
	// SetReplayable records a validation result. Nothing else on a record ever changes.
	SetReplayable(ctx context.Context, id string, v Validation) error
}
