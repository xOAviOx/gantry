// Package migrations embeds the SQL migration files so controld can run them
// via golang-migrate's iofs source without depending on files on disk (D8, D11).
package migrations

import "embed"

//go:embed *.sql
var FS embed.FS
