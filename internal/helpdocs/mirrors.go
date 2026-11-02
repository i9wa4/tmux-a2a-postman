package helpdocs

import "io/fs"

// Mirror describes checked checkout output, never another editing source.
type Mirror struct {
	Source string
	Target string
	Mode   fs.FileMode
}

func Mirrors() []Mirror {
	result := []Mirror{
		{"topics/cli-spec.md", "docs/cli-spec.md", 0o644},
		{"topics/server-status.md", "docs/design/server-status.md", 0o644},
		{"topics/recipient-markdown.md", "docs/design/recipient-markdown-parity.md", 0o644},
		{"topics/external-filesystem-channel.md", "docs/design/external-filesystem-channel.md", 0o644},
		{"topics/invocation-diagnostics.md", "docs/invocation-diagnostics.md", 0o644},
		{"topics/launching.md", "docs/launch-agents.md", 0o644},
		{"assets/diagnostics/analyze-invocations.py", "scripts/validation/analyze-invocations.py", 0o755},
		{"assets/examples/launch/postman.md", "examples/launch/postman.md", 0o644},
	}
	for _, name := range EntryNames() {
		result = append(result, Mirror{"entries/" + name + "/SKILL.md", "skills/" + name + "/SKILL.md", 0o644})
	}
	return result
}

func MirrorData(file Mirror) ([]byte, error) {
	data, err := content.ReadFile(file.Source)
	if err != nil {
		return nil, err
	}
	if len(file.Source) > 7 && file.Source[:7] == "topics/" {
		prefix := "<!-- Generated from internal/helpdocs/" + file.Source + "; edit that source and run go generate ./internal/helpdocs. -->\n\n"
		data = append([]byte(prefix), data...)
	}
	return data, nil
}
