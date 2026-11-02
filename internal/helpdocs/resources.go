package helpdocs

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

type asset struct {
	name string
	data []byte
	mode fs.FileMode
}

// ResourcePath is explicit filesystem access. Read, Topics and status callers
// never call it. Existing application state-root permissions are unrelated.
func ResourcePath(ctx context.Context) (string, error) {
	cache, err := os.UserCacheDir()
	if err != nil || !filepath.IsAbs(cache) {
		return "", errors.New("resources require an absolute user cache directory")
	}
	modes, err := content.ReadFile("asset-modes.json")
	if err != nil {
		return "", err
	}
	return materialize(ctx, cache, BundleID(), Assets(), modes)
}

func assetList(source fs.FS, modeJSON []byte) ([]asset, error) {
	var modes map[string]string
	if err := json.Unmarshal(modeJSON, &modes); err != nil {
		return nil, fmt.Errorf("invalid embedded resource modes: %w", err)
	}
	var result []asset
	visited := make(map[string]bool)
	total := 0
	err := fs.WalkDir(source, ".", func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if visited[name] || len(visited) >= 512 || !fs.ValidPath(name) || strings.ContainsAny(name, "\\\x00") || strings.Count(name, "/") > 16 {
			return fmt.Errorf("invalid or repeated resource path %q", name)
		}
		visited[name] = true
		if entry.IsDir() {
			return nil
		}
		if !fs.ValidPath(name) || strings.ContainsAny(name, "\\\x00") || entry.Type() != 0 {
			return fmt.Errorf("invalid embedded resource %q", name)
		}
		mode := fs.FileMode(0)
		switch modes[name] {
		case "0444":
			mode = 0o444
		case "0555":
			mode = 0o555
		default:
			return fmt.Errorf("resource %q has no valid declared mode", name)
		}
		data, err := fs.ReadFile(source, name)
		if err != nil {
			return err
		}
		total += len(data)
		if len(data) > 1024*1024 || total > 8*1024*1024 || len(result) >= 256 {
			return errors.New("embedded resource set exceeds its bounds")
		}
		result = append(result, asset{name, data, mode})
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(result) != len(modes) || len(result) == 0 {
		return nil, errors.New("embedded resource manifest does not match its files")
	}
	return result, nil
}

func privateSubdir(parent *os.Root, name string) (*os.Root, error) {
	if err := parent.Mkdir(name, 0o700); err != nil && !errors.Is(err, fs.ErrExist) {
		return nil, err
	}
	info, err := parent.Lstat(name)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0o022 != 0 {
		return nil, fmt.Errorf("resource directory %q must be a real directory without group/other writes", name)
	}
	return parent.OpenRoot(name)
}

func materialize(ctx context.Context, cache, identity string, source fs.FS, modes []byte) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if len(identity) != 64 || strings.Trim(identity, "0123456789abcdef") != "" || !filepath.IsAbs(cache) {
		return "", errors.New("invalid resource identity or cache path")
	}
	files, err := assetList(source, modes)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(cache, 0o700); err != nil {
		return "", err
	}
	cache, err = filepath.EvalSymlinks(cache)
	if err != nil {
		return "", err
	}
	cacheRoot, err := os.OpenRoot(cache)
	if err != nil {
		return "", err
	}
	defer func() { _ = cacheRoot.Close() }()
	app, err := privateSubdir(cacheRoot, "tmux-a2a-postman")
	if err != nil {
		return "", err
	}
	defer func() { _ = app.Close() }()
	base, err := privateSubdir(app, "resources")
	if err != nil {
		return "", err
	}
	defer func() { _ = base.Close() }()
	destination := filepath.Join(cache, "tmux-a2a-postman", "resources", identity)
	if _, err := base.Lstat(identity); err == nil {
		if err := validateSet(base, identity, files); err != nil {
			return "", fmt.Errorf("cached resource set is invalid; preserve it for diagnosis and remove it explicitly before retrying: %w", err)
		}
		return destination, nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return "", err
	}
	temporary := ".prepare-" + rand.Text()
	if err := base.Mkdir(temporary, 0o700); err != nil {
		return "", err
	}
	stage, err := base.OpenRoot(temporary)
	if err != nil {
		return "", err
	}
	defer func() {
		_ = stage.Close()
		// Only this invocation's unpublished temporary tree is removable.
		_ = base.RemoveAll(temporary)
	}()
	for _, file := range files {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		if err := stage.MkdirAll(filepath.FromSlash(path.Dir(file.name)), 0o700); err != nil {
			return "", err
		}
		out, err := stage.OpenFile(filepath.FromSlash(file.name), os.O_CREATE|os.O_EXCL|os.O_WRONLY, file.mode)
		if err != nil {
			return "", err
		}
		_, writeErr := out.Write(file.data)
		modeErr := out.Chmod(file.mode)
		syncErr, closeErr := out.Sync(), out.Close()
		if err := errors.Join(writeErr, modeErr, syncErr, closeErr); err != nil {
			return "", err
		}
	}
	if err := validateSet(base, temporary, files); err != nil {
		return "", err
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if err := base.Rename(temporary, identity); err != nil {
		// A concurrent caller may have published the same complete generation.
		if winnerErr := validateSet(base, identity, files); winnerErr != nil {
			return "", errors.Join(err, winnerErr)
		}
	}
	if err := validateSet(base, identity, files); err != nil {
		return "", err
	}
	return destination, nil
}

func validateSet(parent *os.Root, name string, files []asset) error {
	info, err := parent.Lstat(name)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("resource set %q is missing or not a real directory", name)
	}
	root, err := parent.OpenRoot(name)
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	expected := make(map[string]asset, len(files))
	directories := map[string]bool{".": true}
	for _, file := range files {
		expected[file.name] = file
		for directory := path.Dir(file.name); directory != "."; directory = path.Dir(directory) {
			directories[directory] = true
		}
	}
	var seen []string
	err = fs.WalkDir(root.FS(), ".", func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("resource symlink %q is not allowed", name)
		}
		if entry.IsDir() {
			if !directories[name] {
				return fmt.Errorf("unexpected resource directory %q", name)
			}
			return nil
		}
		file, ok := expected[name]
		if !ok || entry.Type() != 0 {
			return fmt.Errorf("unexpected resource %q", name)
		}
		info, err := entry.Info()
		if err != nil || info.Mode().Perm() != file.mode || info.Size() != int64(len(file.data)) {
			return fmt.Errorf("resource metadata mismatch for %q", name)
		}
		data, err := root.ReadFile(filepath.FromSlash(name))
		if err != nil || !bytes.Equal(data, file.data) {
			return fmt.Errorf("resource content mismatch for %q", name)
		}
		seen = append(seen, name)
		return nil
	})
	if err != nil {
		return err
	}
	if len(seen) != len(expected) {
		sort.Strings(seen)
		return fmt.Errorf("incomplete resource set: found %d of %d files", len(seen), len(expected))
	}
	return nil
}
