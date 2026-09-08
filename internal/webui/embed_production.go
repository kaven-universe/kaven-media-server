//go:build webui

package webui

import "embed"

// Production builds require generated assets; a missing dist directory is a
// compile-time error instead of silently embedding the development placeholder.
//
//go:embed all:dist
var assets embed.FS

const assetRoot = "dist"
