// Package migrations embeds foreman's versioned SQL migrations, applied in file-name
// order by internal/adapters/sqlite.
package migrations

import "embed"

// FS holds every *.sql migration.
//
//go:embed *.sql
var FS embed.FS
