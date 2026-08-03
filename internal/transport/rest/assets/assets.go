// Package assets embeds the compiled Vue SPA, so the binary is the whole
// deployable and API and frontend can never be at different versions.
package assets

import (
	"embed"
	"io/fs"
)

// dist is written by `npm run build` in ui/ (see the Makefile). A placeholder
// index.html is committed so `go build` works on a clean checkout.
//
//go:embed all:dist
var dist embed.FS

// Dist returns the built SPA rooted at its index.
func Dist() (fs.FS, error) { return fs.Sub(dist, "dist") }
