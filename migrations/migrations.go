// Package migrations embeds the goose SQL migrations so the binary carries its
// own schema and a deployment is one file.
//
// Migrations are forward-only, numbered as in docs/03-data-model.md §3.7, and
// run explicitly rather than at boot (docs/adr/0011-explicit-migrations.md).
package migrations

import "embed"

// FS holds every migration. Every one has a working `-- +goose Down`; CI runs
// up -> down -> up on a scratch database.
//
//go:embed *.sql
var FS embed.FS
