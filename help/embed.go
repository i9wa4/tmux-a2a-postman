// Package help embeds the canonical command and workflow documentation.
package help

import "embed"

//go:embed *.md */SKILL.md */references/*.md */scripts/*
var Content embed.FS
