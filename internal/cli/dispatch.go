package cli

import "fmt"

type Config struct {
	ConfigPath     string
	ConfigProvided bool
}

type Handlers struct {
	Resources        func([]string) error
	LaunchAgents     func([]string) error
	ChannelSync      func([]string) error
	ChannelReport    func([]string) error
	Pop              func([]string) error
	GetSessionStatus func([]string) error
	GetStatusOneline func([]string) error
	RefreshStatus    func([]string) error
	ExecuteHeredoc   func([]string) error
	ApproveExecution func([]string) error
	DeclineExecution func([]string) error
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
	var handler func([]string) error
	workspaceCommand := true
	switch command {
	case "resources":
		handler = h.Resources
		workspaceCommand = false
	case "launch-agents":
		handler = h.LaunchAgents
	case "channel-sync":
		handler = h.ChannelSync
	case "channel-report-heredoc":
		handler = h.ChannelReport
	case "pop":
		handler = h.Pop
	case "get-status":
		handler = h.GetSessionStatus
	case "refresh-status":
		handler = h.RefreshStatus
	case "get-status-oneline":
		handler = h.GetStatusOneline
	case "execute-heredoc":
		handler = h.ExecuteHeredoc
	case "approve-execution":
		handler = h.ApproveExecution
	case "decline-execution":
		handler = h.DeclineExecution
	case "send-heredoc":
		handler = h.SendHeredoc
	case "version":
		handler = h.Version
		workspaceCommand = false
	case "help":
		h.Help(args)
		return Result{}
	default:
		return Result{Label: "postman", Err: fmt.Errorf("unknown command %q", command), ShowUsage: true}
	}
	if isSubcommandHelpRequest(args) && hasCommandHelpTopic(command) {
		h.Help([]string{command})
		return Result{}
	}
	if workspaceCommand && (cfg.ConfigProvided || cfg.ConfigPath != "") {
		args = append([]string{"--config", cfg.ConfigPath}, args...)
	}
	return Result{Label: "postman " + command, Err: handler(args)}
}
