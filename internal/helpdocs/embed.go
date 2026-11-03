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
	"github.com/i9wa4/tmux-a2a-postman/skills"
)

var roots = []struct {
	name  string
	files fs.FS
}{
	{"help", helpdata.Content},
	{"skills", skills.Content},
	{"config", configdata.Content},
	{"docs", docs.Content},
}

// Read returns a complete authored topic without accessing mutable state.
func Read(topic string) ([]byte, error) {
	if topic == "" {
		topic = "overview"
	}
	if !validName(topic) {
		return nil, fmt.Errorf("unknown help topic %q", topic)
	}
	data, err := helpdata.Content.ReadFile(topic + ".md")
	if err != nil {
		return nil, fmt.Errorf("unknown help topic %q; use tmux-a2a-postman help for installed topics: %w", topic, err)
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
		return nil, fmt.Errorf("unknown Skill %q; use tmux-a2a-postman skills: %w", name, err)
	}
	return data, nil
}

// Reference returns one complete, named reference from an authored Skill.
func Reference(name, ref string) ([]byte, error) {
	if !validName(name) || !validName(ref) {
		return nil, fmt.Errorf("invalid Skill or reference name")
	}
	data, err := fs.ReadFile(Entries(), name+"/references/"+ref+".md")
	if err != nil {
		return nil, fmt.Errorf("unknown reference %q for Skill %q; use tmux-a2a-postman skills %s: %w", ref, name, name, err)
	}
	return data, nil
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
