package testfixture

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strings"
)

// FakeMessage is the message returned by a fake agent's pop callback.
type FakeMessage struct {
	Filename string
	Body     string
}

// FakeAgent reacts to terminal mail notifications. Pop must claim and read the
// archived message before returning; Reply sends the response for that message.
// The terminal reader is the only trigger for Pop.
type FakeAgent struct {
	Node     string
	Terminal io.Reader
	Pop      func() (FakeMessage, error)
	Reply    func(FakeMessage) error
}

// Run consumes terminal lines until EOF or the first pop/reply error.
func (a FakeAgent) Run() error {
	if a.Node == "" || a.Terminal == nil || a.Pop == nil || a.Reply == nil {
		return errors.New("fake agent requires node, terminal, pop, and reply")
	}

	seen := make(map[string]bool)
	scanner := bufio.NewScanner(a.Terminal)
	for scanner.Scan() {
		filename, ok := mailNotificationFilename(scanner.Text(), a.Node)
		if !ok || seen[filename] {
			continue
		}

		message, err := a.Pop()
		if err != nil {
			return fmt.Errorf("pop after notification %q: %w", filename, err)
		}
		if message.Filename != filename {
			return fmt.Errorf("pop after notification %q returned %q", filename, message.Filename)
		}
		if err := a.Reply(message); err != nil {
			return fmt.Errorf("reply to %q: %w", filename, err)
		}
		seen[filename] = true
	}
	return scanner.Err()
}

func mailNotificationFilename(line, node string) (string, bool) {
	prefix := "Hello, " + node + "! You've got mail: "
	const suffix = ". Run `tmux-a2a-postman pop` to claim it and get the archived body path."
	if !strings.HasPrefix(line, prefix) || !strings.HasSuffix(line, suffix) {
		return "", false
	}
	filename := strings.TrimSuffix(strings.TrimPrefix(line, prefix), suffix)
	if filename == "" || !strings.HasSuffix(filename, ".md") || strings.ContainsAny(filename, "/\\") {
		return "", false
	}
	return filename, true
}
