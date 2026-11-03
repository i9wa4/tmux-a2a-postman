// Package docs embeds bundled license notices.
package docs

import "embed"

//go:embed licenses
var Content embed.FS
