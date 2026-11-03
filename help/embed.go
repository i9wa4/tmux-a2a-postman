// Package help embeds the authored CLI documentation.
package help

import "embed"

//go:embed *.md
var Content embed.FS
