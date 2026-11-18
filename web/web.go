// Package web embeds the built frontend (web/dist) into the GPUHub binary.
// Build the frontend first: cd web && npm install && npm run build
package web

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var dist embed.FS

// FS returns the dist subtree rooted at its own directory.
func FS() (fs.FS, error) {
	return fs.Sub(dist, "dist")
}
