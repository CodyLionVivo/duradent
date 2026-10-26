// Package migrations embeds the SQL schema so the binary can migrate itself.
package migrations

import "embed"

//go:embed *.sql
var FS embed.FS
