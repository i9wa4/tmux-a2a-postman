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

	configdata "github.com/i9wa4/tmux-a2a-postman/config"
	"github.com/i9wa4/tmux-a2a-postman/docs"
	helpdata "github.com/i9wa4/tmux-a2a-postman/help"
	skilldata "github.com/i9wa4/tmux-a2a-postman/skills"
)

var roots = []struct {
	name  string
	files fs.FS
}{
	{"help", helpdata.Content},
	{"skills", skilldata.Content},
	{"config", configdata.Content},
	{"docs", docs.Content},
}

// Read returns a complete authored topic without accessing mutable state.
func Read(topic string, refs ...string) ([]byte, error) {
	if topic == "" {
		topic = "overview"
	}
	if !validName(topic) || len(refs) > 1 {
		return nil, fmt.Errorf("unknown help topic %q", topic)
	}
	filename := topic + ".md"
	if len(refs) == 1 {
		if !validName(refs[0]) {
			return nil, fmt.Errorf("invalid help reference %q", refs[0])
		}
		if topic == "config" && refs[0] == "default" {
			return configdata.Content.ReadFile("postman.md")
		}
		filename = topic + "/references/" + refs[0] + ".md"

	}
	data, err := helpdata.Content.ReadFile(filename)
	if err != nil {
		return nil, fmt.Errorf("unknown help topic or reference %q; use tmux-a2a-postman help for installed topics: %w", filename, err)
	}
	return data, nil
}

// Topics is sorted and does not resolve configuration, native tmux or resources.
func Topics() []string {
	entries, _ := helpdata.Content.ReadDir(".")
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".md") {
			names = append(names, strings.TrimSuffix(entry.Name(), ".md"))
		}
	}
	sort.Strings(names)
	return names
}

// SkillNames lists canonical Skill packages for the builtin role catalog.
func SkillNames() []string {
	names, _ := fs.Glob(skilldata.Content, "*/SKILL.md")
	for i := range names {
		names[i] = path.Dir(names[i])
	}
	return names
}

// ReadSkill returns a complete embedded Skill or one of its Markdown references.
func ReadSkill(name string, refs ...string) ([]byte, error) {
	if !validName(name) || len(refs) > 1 {
		return nil, fmt.Errorf("invalid skill name or reference %q", name)
	}
	filename := name + "/SKILL.md"
	if len(refs) == 1 {
		if !validName(refs[0]) {
			return nil, fmt.Errorf("invalid skill reference %q", refs[0])
		}
		filename = name + "/references/" + refs[0] + ".md"
	}
	data, err := skilldata.Content.ReadFile(filename)
	if err != nil {
		return nil, fmt.Errorf("unknown skill or reference %q: %w", filename, err)
	}
	return data, nil
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

type bundledFiles struct{}

func (bundledFiles) Open(name string) (fs.File, error) {
	if fs.ValidPath(name) {
		prefix, relative, ok := strings.Cut(name, "/")
		if ok {
			for _, root := range roots {
				if prefix == root.name {
					return root.files.Open(relative)
				}
			}
		}
	}
	return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
}

// Assets reads canonical repository-relative paths from the embedded packages.
// The manifest selects which files need explicit filesystem materialization.
func Assets() fs.FS {
	return bundledFiles{}
}

// BundleID binds paths and bytes of every embedded topic, entry and asset. It
// does not trust release labels: different development builds cannot collide.
func BundleID() string {
	hash := sha256.New()
	for _, root := range roots {
		_ = fs.WalkDir(root.files, ".", func(name string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() {
				return nil
			}
			data, readErr := fs.ReadFile(root.files, name)
			if readErr != nil {
				return readErr
			}
			_, _ = fmt.Fprintf(hash, "%s\x00%d\x00", path.Join(root.name, name), len(data))
			_, _ = hash.Write(data)
			return nil
		})
	}
	return hex.EncodeToString(hash.Sum(nil))
}
