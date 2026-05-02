package store

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// PendingPost is a live message request waiting in a session post/ directory.
type PendingPost struct {
	SessionDir string
	PostDir    string
	Filename   string
	Path       string
}

// PostFromPath returns the pending-post record for a concrete post/ path.
func PostFromPath(path string) (PendingPost, bool) {
	filename := filepath.Base(path)
	if !strings.HasSuffix(filename, ".md") {
		return PendingPost{}, false
	}
	postDir := filepath.Dir(path)
	if filepath.Base(postDir) != "post" {
		return PendingPost{}, false
	}
	return PendingPost{
		SessionDir: filepath.Dir(postDir),
		PostDir:    postDir,
		Filename:   filename,
		Path:       path,
	}, true
}

// ListPendingPosts scans session post/ directories for live .md requests.
func ListPendingPosts(sessionDirs []string) ([]PendingPost, error) {
	posts := make([]PendingPost, 0)
	for _, sessionDir := range sessionDirs {
		postDir := filepath.Join(sessionDir, "post")
		entries, err := os.ReadDir(postDir)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, fmt.Errorf("scan pending post dir %s: %w", postDir, err)
		}
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
				continue
			}
			posts = append(posts, PendingPost{
				SessionDir: sessionDir,
				PostDir:    postDir,
				Filename:   entry.Name(),
				Path:       filepath.Join(postDir, entry.Name()),
			})
		}
	}

	sort.Slice(posts, func(i, j int) bool {
		if posts[i].SessionDir != posts[j].SessionDir {
			return posts[i].SessionDir < posts[j].SessionDir
		}
		return posts[i].Filename < posts[j].Filename
	})
	return posts, nil
}
