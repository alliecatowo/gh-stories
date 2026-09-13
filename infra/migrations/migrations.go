// Package migrations embeds the canonical versioned SQL history so that a
// single binary can migrate its own database. The .sql files are the source of
// truth and are also runnable with plain psql.
package migrations

import "embed"

//go:embed *.sql
var FS embed.FS
