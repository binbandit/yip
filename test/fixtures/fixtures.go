// Package fixtures embeds the sample repositories used by the demo and by
// integration tests. They are fixtures, not real projects.
package fixtures

import "embed"

//go:embed all:testdata/repos
var Repos embed.FS
