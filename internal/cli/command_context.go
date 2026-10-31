package cli

import (
	"context"
	"errors"
	"io"
	"os"
	"os/signal"
	"syscall"
)

func defaultInterruptContext() (context.Context, func()) {
	return signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
}

func defaultStdinIsTerminal(stdin io.Reader) bool {
	f, ok := stdin.(*os.File)
	if !ok {
		return false
	}
	i, e := f.Stat()
	return e == nil && i.Mode()&os.ModeCharDevice != 0
}

func sendHeredocBodySourceError() error {
	return errors.New("message body is required on non-terminal stdin from a quoted heredoc")
}
