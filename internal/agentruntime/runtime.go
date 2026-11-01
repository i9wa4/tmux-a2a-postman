package agentruntime

import "strings"

const (
	Unknown = "unknown"
	Claude  = "claude"
	Codex   = "codex"
)

type Definition struct {
	ID                   string
	ProductName          string
	ConventionalSkillDir string
}

var supported = []Definition{
	{
		ID:                   Claude,
		ProductName:          "Claude Code",
		ConventionalSkillDir: "$HOME/.claude/skills",
	},
	{
		ID:                   Codex,
		ProductName:          "Codex CLI",
		ConventionalSkillDir: "$HOME/.codex/skills",
	},
}

func Normalize(runtime string) string {
	return strings.ToLower(strings.TrimSpace(runtime))
}

func Supported() []Definition {
	defs := make([]Definition, len(supported))
	copy(defs, supported)
	return defs
}

func Lookup(runtime string) (Definition, bool) {
	runtime = Normalize(runtime)
	for _, definition := range supported {
		if definition.ID == runtime {
			return definition, true
		}
	}
	return Definition{}, false
}

func IsSupported(runtime string) bool {
	_, ok := Lookup(runtime)
	return ok
}
