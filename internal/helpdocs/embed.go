// Package helpdocs owns the binary's canonical Markdown and offline assets.
package helpdocs

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"

	"github.com/i9wa4/tmux-a2a-postman/docs"
	"github.com/i9wa4/tmux-a2a-postman/skills"
)

var content = docs.Content

// Existing guides and design references are served from their authored paths.
var topicFiles = map[string]string{
	"cli-spec":                    "cli-spec.md",
	"server-status":               "design/server-status.md",
	"recipient-markdown":          "design/recipient-markdown-parity.md",
	"external-filesystem-channel": "design/external-filesystem-channel.md",
	"invocation-diagnostics":      "invocation-diagnostics.md",
	"launching":                   "launch-agents.md",
}

// Read returns a complete authored topic without accessing mutable state.
func Read(topic string) ([]byte, error) {
	if topic == "" {
		topic = "overview"
	}
	if !validName(topic) {
		return nil, fmt.Errorf("unknown help topic %q", topic)
	}
	name := topicFiles[topic]
	if name == "" {
		name = "help/" + topic + ".md"
	}
	data, err := content.ReadFile(name)
	if err != nil {
		return nil, fmt.Errorf("unknown help topic %q; use tmux-a2a-postman help for installed topics: %w", topic, err)
	}
	return data, nil
}

// Topics is sorted and does not resolve configuration, native tmux or resources.
func Topics() []string {
	entries, _ := content.ReadDir("help")
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".md") {
			names = append(names, strings.TrimSuffix(entry.Name(), ".md"))
		}
	}
	for name := range topicFiles {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func validName(name string) bool {
	if name == "" || len(name) > 80 {
		return false
	}
	for _, r := range name {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '-' {
			return false
		}
	}
	return true
}

// Entries supplies the same authored thin Skills used by declarative installs.
func Entries() fs.FS {
	return skills.Content
}

// EntryNames lists the authored entries without inspecting client directories.
func EntryNames() []string {
	entries, _ := fs.ReadDir(Entries(), ".")
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			names = append(names, entry.Name())
		}
	}
	return names
}

// Entry provides complete Markdown; installation belongs to the consumer.
func Entry(name string) ([]byte, error) {
	if !validName(name) {
		return nil, fmt.Errorf("invalid Skill name %q", name)
	}
	data, err := fs.ReadFile(Entries(), name+"/SKILL.md")
	if err != nil {
		return nil, fmt.Errorf("unknown Skill %q; use skills list: %w", name, err)
	}
	return data, nil
}

// Assets are the only files eligible for explicit resource materialization.
func Assets() fs.FS {
	result, _ := fs.Sub(content, "assets")
	return result
}

// BundleID binds paths and bytes of every embedded topic, entry and asset. It
// does not trust release labels: different development builds cannot collide.
func BundleID() string {
	hash := sha256.New()
	for _, source := range []fs.FS{content, skills.Content} {
		_ = fs.WalkDir(source, ".", func(name string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() {
				return nil
			}
			data, readErr := fs.ReadFile(source, name)
			if readErr != nil {
				return readErr
			}
			_, _ = fmt.Fprintf(hash, "%s\x00%d\x00", path.Clean(name), len(data))
			_, _ = hash.Write(data)
			return nil
		})
	}
	return hex.EncodeToString(hash.Sum(nil))
}
