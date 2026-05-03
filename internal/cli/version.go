package cli

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/i9wa4/tmux-a2a-postman/internal/version"
)

type versionOutput struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Commit  string `json:"commit"`
}

func RunVersion(w io.Writer, args []string) error {
	if len(args) != 0 {
		return fmt.Errorf("version takes no arguments")
	}
	return json.NewEncoder(w).Encode(versionOutput{
		Name:    "tmux-a2a-postman",
		Version: version.Version,
		Commit:  version.Commit,
	})
}
