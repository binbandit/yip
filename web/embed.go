// Package web embeds the built Svelte client (web/dist) into the hub binary.
// Build it with `make web`; without it the hub serves a placeholder page.
package web

import "embed"

//go:embed all:dist
var Dist embed.FS
