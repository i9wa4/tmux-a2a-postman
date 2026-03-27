package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/i9wa4/tmux-a2a-postman/internal/config"
	"github.com/i9wa4/tmux-a2a-postman/internal/discovery"
	"github.com/i9wa4/tmux-a2a-postman/internal/ping"
)

// idempotencyKeyPattern is the canonical regex for --idempotency-key tokens.
// Prevents newline/YAML injection via caller-supplied token.
const idempotencyKeyPattern = `^[a-zA-Z0-9][a-zA-Z0-9_-]{0,127}$`

var validIdempotencyKeyRe = regexp.MustCompile(idempotencyKeyPattern)

// alwaysExcludedParams is the set of flag names excluded from --params scope
// for ALL commands. These are security/semantics guards.
var alwaysExcludedParams = map[string]bool{
	"context-id": true, // Security: context redirect risk
	"config":     true, // Security: config path injection
	"session":    true, // Security: session hijack risk
	"from":       true, // Security: sender identity spoofing
	"bindings":   true, // Security: binding injection
	"file":       true, // Security: arbitrary filesystem path (same class as config)
}

// perCommandExcludedParams maps command name to additional excluded flag names.
var perCommandExcludedParams = map[string]map[string]bool{}

// isExcludedParam returns true if key is excluded from --params scope for the
// given command. Checks always-excluded list first, then per-command table.
func isExcludedParam(key, command string) bool {
	if alwaysExcludedParams[key] {
		return true
	}
	if perMap, ok := perCommandExcludedParams[command]; ok {
		return perMap[key]
	}
	return false
}

// looksLikeJSON reports whether s (trimmed) begins with '{'.
func looksLikeJSON(s string) bool {
	t := strings.TrimSpace(s)
	return len(t) > 0 && t[0] == '{'
}

// parseShorthand parses "key=value,key=value" shorthand into map[string]string.
// Splits on first '=' only; values may contain additional '=' characters.
func parseShorthand(raw string) (map[string]string, error) {
	result := make(map[string]string)
	for _, pair := range strings.Split(raw, ",") {
		parts := strings.SplitN(pair, "=", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf(
				"invalid shorthand pair %q: missing = separator"+
					" (values containing commas require JSON form:"+
					` --params '{"key":"val,with,commas"}')`,
				pair,
			)
		}
		result[parts[0]] = parts[1]
	}
	return result, nil
}

// parseParams parses a --params value (shorthand or JSON) into map[string]string.
// JSON path uses dec.UseNumber() to preserve integer literals (e.g., "1000000"
// not "1e+06"). Type-switch rejects non-scalar values (arrays, objects, null).
func parseParams(raw string) (map[string]string, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	var result map[string]interface{}
	if looksLikeJSON(raw) {
		dec := json.NewDecoder(strings.NewReader(raw))
		dec.UseNumber()
		if err := dec.Decode(&result); err != nil {
			return nil, fmt.Errorf("--params JSON parse error: %w", err)
		}
	} else {
		kv, err := parseShorthand(raw)
		if err != nil {
			return nil, fmt.Errorf("--params: %w", err)
		}
		result = make(map[string]interface{}, len(kv))
		for k, v := range kv {
			result[k] = v
		}
	}
	out := make(map[string]string, len(result))
	for k, v := range result {
		switch val := v.(type) {
		case json.Number:
			out[k] = val.String()
		case string:
			out[k] = val
		case bool:
			out[k] = fmt.Sprint(val)
		case nil:
			return nil, fmt.Errorf("--params: field %q must be a scalar value, not null", k)
		default:
			return nil, fmt.Errorf("--params: field %q must be scalar, got %T", k, v)
		}
	}
	return out, nil
}

// applyParams applies resolvedParams to fs for flags not in explicitlySet.
// commandName is passed to isExcludedParam to enforce per-command exclusions.
// Returns a hard error if any key is on the excluded list.
func applyParams(fs *flag.FlagSet, resolvedParams map[string]string, explicitlySet map[string]bool, commandName string) error {
	for key, strVal := range resolvedParams {
		if isExcludedParam(key, commandName) {
			return fmt.Errorf("--params: field %q is not settable via --params", key)
		}
		if !explicitlySet[key] {
			if err := fs.Set(key, strVal); err != nil {
				return fmt.Errorf("--params: invalid value for %q: %w", key, err)
			}
		}
	}
	return nil
}

// resolveInboxPath resolves the inbox path for the current node (#196).
func resolveInboxPath(args []string) (string, error) {
	fs := flag.NewFlagSet("inbox-resolve", flag.ContinueOnError)
	contextID := fs.String("context-id", "", "context ID")
	configPath := fs.String("config", "", "path to config file")
	if err := fs.Parse(args); err != nil {
		return "", err
	}

	cfg, err := config.LoadConfig(*configPath)
	if err != nil {
		return "", fmt.Errorf("loading config: %w", err)
	}

	baseDir := config.ResolveBaseDir(cfg.BaseDir)

	nodeName := config.GetTmuxPaneName()
	if nodeName == "" {
		return "", fmt.Errorf("node name auto-detection failed: set tmux pane title")
	}

	sessionName := config.GetTmuxSessionName()
	if sessionName == "" {
		return "", fmt.Errorf("tmux session name required (run inside tmux)")
	}
	sessionName, err = config.ValidateSessionName(sessionName)
	if err != nil {
		return "", err
	}

	var resolvedContextID string
	if *contextID != "" {
		resolvedContextID, err = config.ResolveContextID(*contextID)
		if err != nil {
			return "", err
		}
	} else {
		resolvedContextID, err = config.ResolveContextIDFromSession(baseDir, sessionName)
		if err != nil {
			return "", err
		}
	}

	inboxPath := filepath.Join(baseDir, resolvedContextID, sessionName, "inbox", nodeName)
	return inboxPath, nil
}

// filterToUINode narrows nodes to the single entry whose simple name matches
// uiNode. If uiNode is empty, a shallow copy of nodes is returned.
// Returns an empty map when uiNode is set but not found.
// NOTE: always returns a new map — callers may mutate freely.
func filterToUINode(nodes map[string]discovery.NodeInfo, uiNode string) map[string]discovery.NodeInfo {
	result := make(map[string]discovery.NodeInfo, len(nodes))
	for nodeName, info := range nodes {
		if uiNode == "" || ping.ExtractSimpleName(nodeName) == uiNode {
			result[nodeName] = info
		}
	}
	return result
}

// printDoubleDashDefaults prints flag defaults with -- prefix (POSIX style).
func printDoubleDashDefaults(fs *flag.FlagSet) {
	fs.VisitAll(func(f *flag.Flag) {
		typeName, usage := flag.UnquoteUsage(f)
		var line string
		if typeName == "" {
			line = fmt.Sprintf("  --%s", f.Name)
		} else {
			line = fmt.Sprintf("  --%s %s", f.Name, typeName)
		}
		fmt.Fprintf(os.Stderr, "%s\n\t\t%s\n", line, usage)
	})
}
