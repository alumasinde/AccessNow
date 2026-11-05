// Package migrations embeds the validated SQL folder so the binary applies the same
// files the CLI would.
package migrations

import "embed"

//go:embed *.sql
var files embed.FS

// FS returns the embedded migration files, named 0001_..._thing.up.sql / .down.sql.
func FS() embed.FS { return files }
