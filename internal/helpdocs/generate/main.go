//go:build ignore

// Regenerate checked public entry points and readable documentation mirrors.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/i9wa4/tmux-a2a-postman/internal/helpdocs"
)

func main() {
	root := flag.String("root", "../..", "repository root")
	flag.Parse()
	if err := generate(*root); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func generate(root string) error {
	for _, file := range helpdocs.Mirrors() {
		data, err := helpdocs.MirrorData(file)
		if err != nil {
			return err
		}
		target := filepath.Join(root, filepath.FromSlash(file.Target))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(target, data, file.Mode); err != nil {
			return err
		}
	}
	return nil
}
