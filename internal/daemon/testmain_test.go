package daemon

import (
	"os"
	"testing"

	"github.com/i9wa4/tmux-a2a-postman/internal/journal"
)

func TestMain(m *testing.M) {
	restoreDurableWrites := journal.SetDurableWritesForTesting(false)
	code := m.Run()
	restoreDurableWrites()
	os.Exit(code)
}
