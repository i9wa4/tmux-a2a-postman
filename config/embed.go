// Package config embeds shipped configuration and the resource manifest.
package config

import "embed"

//go:embed *.json
var Content embed.FS
