// Package migrations embeds the versioned database schema changes.
package migrations

import "embed"

// Files contains SQL migrations compiled into the migration command.
//
//go:embed *.sql
var Files embed.FS
