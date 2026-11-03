// Package skills embeds the same authored entries installed by GitHub CLI or Nix.
package skills

import "embed"

//go:embed */SKILL.md
var Content embed.FS
