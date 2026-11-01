package cli

import "fmt"

type Config struct{ ConfigPath string }

type Handlers struct {
	Pop              func([]string) error
	GetSessionStatus func([]string) error
	ExecuteBash      func([]string) error
	SendHeredoc      func([]string) error
	Version          func([]string) error
	Help             func([]string)
}

type Result struct {
	Label     string
	Err       error
	ShowUsage bool
}

func Dispatch(command string, args []string, cfg Config, h Handlers) Result {
	if isSubcommandHelpRequest(args) && hasCommandHelpTopic(command) {
		h.Help([]string{command})
		return Result{}
	}
	var handler func([]string) error
	switch command {
	case "pop":
		handler = h.Pop
	case "get-status":
		handler = h.GetSessionStatus
	case "execute-bash":
		handler = h.ExecuteBash
	case "send-heredoc":
		handler = h.SendHeredoc
	case "version":
		return Result{Label: "postman version", Err: h.Version(args)}
	case "help":
		h.Help(args)
		return Result{}
	default:
		return Result{Label: "postman", Err: fmt.Errorf("unknown command %q", command), ShowUsage: true}
	}
	if cfg.ConfigPath != "" {
		args = append([]string{"--config", cfg.ConfigPath}, args...)
	}
	return Result{Label: "postman " + command, Err: handler(args)}
}
