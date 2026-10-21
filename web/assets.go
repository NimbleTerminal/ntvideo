// Package web publishes ntvideo's browser host assets, independent of its Go
// playback backend. Assets contains only runtime files, relative to this package.
package web

import "embed"

// Assets can be served directly or exported with the ntvideo-assets tool.
//
//go:embed surface.js
var Assets embed.FS
