package cli

import "fmt"

type Config struct {
	ContextID   string
	ConfigPath  string
	LogFilePath string
	NoTUI       bool
}

type Handlers struct {
	Start                   func(contextID, configPath, logFilePath string, noTUI bool) error
	GetSessionStatusOneline func(args []string) error
	Read                    func(args []string) error
	Pop                     func(args []string) error
	GetSessionHealth        func(args []string) error
	GetContextID            func(args []string) error
	SupervisorDrain         func(args []string) error
	SendMessage             func(args []string) error
	Stop                    func(args []string) error
	Bind                    func(args []string) error
	Schema                  func(args []string) error
	Help                    func(args []string)
}

type Result struct {
	Label     string
	Err       error
	ShowUsage bool
}

func Dispatch(command string, args []string, cfg Config, handlers Handlers) Result {
	switch command {
	case "start":
		return Result{
			Label: "postman start",
			Err:   handlers.Start(cfg.ContextID, cfg.ConfigPath, cfg.LogFilePath, cfg.NoTUI),
		}
	case "get-health-oneline", "get-session-status-oneline":
		return Result{
			Label: "postman get-health-oneline",
			Err:   handlers.GetSessionStatusOneline(args),
		}
	case "read":
		return Result{
			Label: "postman read",
			Err:   handlers.Read(prependConfig(cfg.ConfigPath, prependContextID(cfg.ContextID, args))),
		}
	case "pop":
		return Result{
			Label: "postman pop",
			Err:   handlers.Pop(prependConfig(cfg.ConfigPath, prependContextID(cfg.ContextID, args))),
		}
	case "get-health", "get-session-health":
		return Result{
			Label: "postman get-health",
			Err:   handlers.GetSessionHealth(prependConfig(cfg.ConfigPath, prependContextID(cfg.ContextID, args))),
		}
	case "get-context-id":
		return Result{
			Label: "postman get-context-id",
			Err:   handlers.GetContextID(prependConfig(cfg.ConfigPath, args)),
		}
	case "supervisor-drain":
		return Result{
			Label: "postman supervisor-drain",
			Err:   handlers.SupervisorDrain(prependConfig(cfg.ConfigPath, prependContextID(cfg.ContextID, args))),
		}
	case "send", "send-message":
		return Result{
			Label: "postman send",
			Err:   handlers.SendMessage(prependConfig(cfg.ConfigPath, prependContextID(cfg.ContextID, args))),
		}
	case "stop":
		return Result{
			Label: "postman stop",
			Err:   handlers.Stop(prependConfig(cfg.ConfigPath, args)),
		}
	case "bind":
		return Result{
			Label: "postman bind",
			Err:   handlers.Bind(args),
		}
	case "schema":
		return Result{
			Label: "postman schema",
			Err:   handlers.Schema(args),
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
