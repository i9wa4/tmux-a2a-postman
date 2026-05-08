package journal

import (
	"os"
	"testing"
)

func TestMain(m *testing.M) {
	restoreDurableWrites := SetDurableWritesForTesting(false)
	code := m.Run()
	restoreDurableWrites()
	os.Exit(code)
}
