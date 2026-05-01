package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMain(m *testing.M) {
	tmpDir, err := os.MkdirTemp("", "postman-config-tests-*")
	if err != nil {
		panic(err)
	}
	origWd, err := os.Getwd()
	if err != nil {
		_ = os.RemoveAll(tmpDir)
		panic(err)
	}

	_ = os.Setenv("HOME", filepath.Join(tmpDir, "home"))
	_ = os.Setenv("XDG_CONFIG_HOME", filepath.Join(tmpDir, "xdg"))
	_ = os.Setenv("XDG_STATE_HOME", filepath.Join(tmpDir, "state"))
	_ = os.Setenv("POSTMAN_HOME", "")
	if err := os.Chdir(tmpDir); err != nil {
		_ = os.RemoveAll(tmpDir)
		panic(err)
	}

	code := m.Run()

	_ = os.Chdir(origWd)
	_ = os.RemoveAll(tmpDir)
	os.Exit(code)
}
