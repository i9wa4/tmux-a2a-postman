package cli

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/i9wa4/tmux-a2a-postman/internal/cliutil"
	"github.com/i9wa4/tmux-a2a-postman/internal/config"
	"github.com/i9wa4/tmux-a2a-postman/internal/ping"
	"github.com/i9wa4/tmux-a2a-postman/internal/todo"
)

func RunTodo(args []string) error {
	globalContextID, globalConfigPath, remaining, err := parseTodoGlobalFlags(args)
	if err != nil {
		return err
	}
	if len(remaining) == 0 {
		return fmt.Errorf("todo subcommand required: summary, show, or write")
	}

	switch remaining[0] {
	case "summary":
		return runTodoSummary(remaining[1:], globalContextID, globalConfigPath)
	case "show":
		return runTodoShow(remaining[1:], globalContextID, globalConfigPath)
	case "write":
		return runTodoWrite(remaining[1:], globalContextID, globalConfigPath)
	default:
		return fmt.Errorf("unknown todo subcommand %q", remaining[0])
	}
}

func parseTodoGlobalFlags(args []string) (string, string, []string, error) {
	var contextID string
	var configPath string
	remaining := args
	for len(remaining) > 0 {
		switch {
		case remaining[0] == "--context-id":
			if len(remaining) < 2 {
				return "", "", nil, fmt.Errorf("flag needs an argument: --context-id")
			}
			contextID = remaining[1]
			remaining = remaining[2:]
		case strings.HasPrefix(remaining[0], "--context-id="):
			contextID = strings.TrimPrefix(remaining[0], "--context-id=")
			remaining = remaining[1:]
		case remaining[0] == "--config":
			if len(remaining) < 2 {
				return "", "", nil, fmt.Errorf("flag needs an argument: --config")
			}
			configPath = remaining[1]
			remaining = remaining[2:]
		case strings.HasPrefix(remaining[0], "--config="):
			configPath = strings.TrimPrefix(remaining[0], "--config=")
			remaining = remaining[1:]
		default:
			return contextID, configPath, remaining, nil
		}
	}
	return contextID, configPath, remaining, nil
}

func runTodoSummary(args []string, globalContextID, globalConfigPath string) error {
	fs := flag.NewFlagSet("todo summary", flag.ContinueOnError)
	cliutil.SetUsageWithoutContextID(fs)
	jsonOut := fs.Bool("json", false, "output json")
	contextID := fs.String("context-id", "", "context ID")
	configPath := fs.String("config", "", "path to config file (optional)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	resolvedContextID := pickTodoFlagValue(*contextID, globalContextID)
	resolvedConfigPath := pickTodoFlagValue(*configPath, globalConfigPath)
	sessionDir, cfg, err := resolveTodoSessionDir(resolvedContextID, resolvedConfigPath)
	if err != nil {
		return err
	}
	summaries, err := todo.Summaries(sessionDir, cfg.OrderedNodeNames())
	if err != nil {
		return err
	}
	if *jsonOut {
		type summaryJSON struct {
			Node    string `json:"node"`
			Token   string `json:"token"`
			State   string `json:"state"`
			Checked int    `json:"checked"`
			Total   int    `json:"total"`
			Exists  bool   `json:"exists"`
			Invalid bool   `json:"invalid"`
		}
		response := struct {
			Nodes []summaryJSON `json:"nodes"`
		}{Nodes: make([]summaryJSON, 0, len(summaries))}
		for _, summary := range summaries {
			response.Nodes = append(response.Nodes, summaryJSON{
				Node:    summary.Node,
				Token:   summary.Token(),
				State:   summary.State(),
				Checked: summary.Checked,
				Total:   summary.Total,
				Exists:  summary.Exists,
				Invalid: summary.Invalid,
			})
		}
		return json.NewEncoder(os.Stdout).Encode(response)
	}

	for _, summary := range summaries {
		if summary.Invalid {
			fmt.Fprintf(os.Stdout, "%s %s invalid\n", summary.Node, summary.Token())
			continue
		}
		fmt.Fprintf(os.Stdout, "%s %s %d/%d\n", summary.Node, summary.Token(), summary.Checked, summary.Total)
	}
	return nil
}

func runTodoShow(args []string, globalContextID, globalConfigPath string) error {
	fs := flag.NewFlagSet("todo show", flag.ContinueOnError)
	cliutil.SetUsageWithoutContextID(fs)
	node := fs.String("node", "", "node name (defaults to current pane title)")
	contextID := fs.String("context-id", "", "context ID")
	configPath := fs.String("config", "", "path to config file (optional)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	resolvedContextID := pickTodoFlagValue(*contextID, globalContextID)
	resolvedConfigPath := pickTodoFlagValue(*configPath, globalConfigPath)
	sessionDir, _, err := resolveTodoSessionDir(resolvedContextID, resolvedConfigPath)
	if err != nil {
		return err
	}
	targetNode := *node
	if targetNode == "" {
		targetNode, err = currentTodoNodeName()
		if err != nil {
			return err
		}
	}
	content, err := todo.ReadFile(sessionDir, targetNode)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	fmt.Print(content)
	return nil
}

func runTodoWrite(args []string, globalContextID, globalConfigPath string) error {
	fs := flag.NewFlagSet("todo write", flag.ContinueOnError)
	cliutil.SetUsageWithoutContextID(fs)
	body := fs.String("body", "", "replacement document body")
	filePath := fs.String("file", "", "replacement document path")
	contextID := fs.String("context-id", "", "context ID")
	configPath := fs.String("config", "", "path to config file (optional)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	explicit := make(map[string]bool)
	fs.Visit(func(f *flag.Flag) {
		explicit[f.Name] = true
	})
	bodySet := explicit["body"]
	fileSet := explicit["file"]
	switch {
	case bodySet == fileSet:
		return fmt.Errorf("todo write requires exactly one of --body or --file")
	}

	resolvedContextID := pickTodoFlagValue(*contextID, globalContextID)
	resolvedConfigPath := pickTodoFlagValue(*configPath, globalConfigPath)
	sessionDir, _, err := resolveTodoSessionDir(resolvedContextID, resolvedConfigPath)
	if err != nil {
		return err
	}
	ownerNode, err := currentTodoNodeName()
	if err != nil {
		return err
	}

	content := *body
	if fileSet {
		data, err := os.ReadFile(*filePath)
		if err != nil {
			return err
		}
		content = string(data)
	}
	return todo.WriteOwnerFile(sessionDir, ownerNode, ownerNode, content)
}

func resolveTodoSessionDir(contextID, configPath string) (string, *config.Config, error) {
	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		return "", nil, fmt.Errorf("loading config: %w", err)
	}
	baseDir := config.ResolveBaseDir(cfg.BaseDir)
	sessionName := config.GetTmuxSessionName()
	if sessionName == "" {
		return "", nil, fmt.Errorf("tmux session name required (run inside tmux)")
	}
	sessionName, err = config.ValidateSessionName(sessionName)
	if err != nil {
		return "", nil, err
	}
	resolvedContextID := contextID
	if resolvedContextID == "" {
		resolvedContextID, err = config.ResolveContextIDFromSession(baseDir, sessionName)
		if err != nil {
			return "", nil, err
		}
	} else {
		resolvedContextID, err = config.ResolveContextID(resolvedContextID)
		if err != nil {
			return "", nil, err
		}
	}
	return filepath.Join(baseDir, resolvedContextID, sessionName), cfg, nil
}

func currentTodoNodeName() (string, error) {
	nodeName := config.GetTmuxPaneName()
	if nodeName == "" {
		return "", fmt.Errorf("node name auto-detection failed: set tmux pane title")
	}
	return ping.ExtractSimpleName(nodeName), nil
}

func pickTodoFlagValue(primary, fallback string) string {
	if primary != "" {
		return primary
	}
	return fallback
}
