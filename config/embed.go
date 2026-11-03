// Package config embeds the standard configuration and resource manifest.
package config

import "embed"

//go:embed *.json postman.md
var Content embed.FS
