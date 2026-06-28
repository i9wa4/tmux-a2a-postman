package cli

import "fmt"

type Config struct {
	ContextID  string
	ConfigPath string
}

type Handlers struct {
	Start                   func(contextID, configPath string) error
	Pop                     func(args []string) error
	CaptureProfile          func(args []string) error
	GetSessionStatus        func(args []string) error
	GetSessionStatusOneline func(args []string) error
	InspectInput            func(args []string) error
	InspectMessage          func(args []string) error
	InspectDaemonSubmit     func(args []string) error
	SendMessage             func(args []string) error
	SendHeredoc             func(args []string) error
	Stop                    func(args []string) error
	Version                 func(args []string) error
	Help                    func(args []string)
}

type Result struct {
	Label     string
	Err       error
	ShowUsage bool
}

func Dispatch(command string, args []string, cfg Config, handlers Handlers) Result {
	if isSubcommandHelpRequest(args) && hasCommandHelpTopic(command) {
		if handlers.Help == nil {
			return Result{
				Label: "postman " + command,
				Err:   fmt.Errorf("help handler is not configured"),
			}
		}
		handlers.Help([]string{command})
		return Result{}
	}

	switch command {
	case "start":
		return Result{
			Label: "postman start",
			Err:   handlers.Start(cfg.ContextID, cfg.ConfigPath),
		}
	case "capture-profile":
		return Result{
			Label: "postman capture-profile",
			Err:   handlers.CaptureProfile(prependConfig(cfg.ConfigPath, prependContextID(cfg.ContextID, args))),
		}
	case "pop":
		return Result{
			Label: "postman pop",
			Err:   handlers.Pop(prependConfig(cfg.ConfigPath, prependContextID(cfg.ContextID, args))),
		}
	case "get-status":
		return Result{
			Label: "postman get-status",
			Err:   handlers.GetSessionStatus(prependConfig(cfg.ConfigPath, prependContextID(cfg.ContextID, args))),
		}
	case "get-status-oneline":
		return Result{
			Label: "postman get-status-oneline",
			Err:   handlers.GetSessionStatusOneline(prependConfig(cfg.ConfigPath, prependContextID(cfg.ContextID, args))),
		}
	case "inspect-input":
		return Result{
			Label: "postman inspect-input",
			Err:   handlers.InspectInput(prependConfig(cfg.ConfigPath, prependContextID(cfg.ContextID, args))),
		}
	case "inspect-message":
		return Result{
			Label: "postman inspect-message",
			Err:   handlers.InspectMessage(prependConfig(cfg.ConfigPath, prependContextID(cfg.ContextID, args))),
		}
	case "inspect-daemon-submit":
		return Result{
			Label: "postman inspect-daemon-submit",
			Err:   handlers.InspectDaemonSubmit(prependConfig(cfg.ConfigPath, prependContextID(cfg.ContextID, args))),
		}
	case "send":
		return Result{
			Label: "postman send",
			Err:   handlers.SendMessage(prependConfig(cfg.ConfigPath, prependContextID(cfg.ContextID, args))),
		}
	case "send-heredoc":
		return Result{
			Label: "postman send-heredoc",
			Err:   handlers.SendHeredoc(prependConfig(cfg.ConfigPath, prependContextID(cfg.ContextID, args))),
		}
	case "stop":
		return Result{
			Label: "postman stop",
			Err:   handlers.Stop(prependConfig(cfg.ConfigPath, args)),
		}
	case "version":
		return Result{
			Label: "postman version",
			Err:   handlers.Version(args),
		}
	case "help":
		handlers.Help(args)
		return Result{}
	default:
		return Result{
			Label:     "postman",
			Err:       fmt.Errorf("unknown command %q", command),
			ShowUsage: true,
		}
	}
}

func prependContextID(contextID string, args []string) []string {
	if contextID == "" {
		return args
	}
	return append([]string{"--context-id", contextID}, args...)
}

func prependConfig(configPath string, args []string) []string {
	if configPath == "" {
		return args
	}
	return append([]string{"--config", configPath}, args...)
}
