// Package skills embeds the same authored Skill packages installed by GitHub CLI or Nix.
package skills

import "embed"

//go:embed */SKILL.md */references/*.md */scripts/*
var Content embed.FS
