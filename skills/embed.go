// Package skills embeds the complete canonical Skill packages.
package skills

import "embed"

//go:embed */SKILL.md */references/*.md */scripts/*
var Content embed.FS
