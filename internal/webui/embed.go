//go:build !webui

package webui

import "embed"

//go:embed fallback/*
var assets embed.FS

const assetRoot = "fallback"
