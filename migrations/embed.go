// Package migrations embeds the SQL migration files so they ship inside the
// compiled binary and can be applied on startup without external files.
package migrations

import "embed"

// FS holds all *.sql migration files in this directory.
//
//go:embed *.sql
var FS embed.FS
