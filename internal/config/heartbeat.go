package config

import (
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

func mappingValue(node *yaml.Node, key string) *yaml.Node {
	if node == nil || node.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Value == key {
			return node.Content[i+1]
		}
	}
	return nil
}

// parseHeartbeatDefaults reads only the selected configuration. A custom file
// never inherits the embedded heartbeat body or interval.
func parseHeartbeatDefaults(content string) (string, string, error) {
	lines := strings.Split(content, "\n")
	start, end, ok, err := frontmatterBounds(lines)
	if err != nil || !ok {
		return "", "", err
	}
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(strings.Join(lines[start+1:end], "\n")+"\n"), &doc); err != nil {
		return "", "", err
	}
	if len(doc.Content) == 0 {
		return "", "", nil
	}
	daemon := mappingValue(doc.Content[0], "daemon")
	if daemon == nil {
		return "", "", nil
	}
	defaults := mappingValue(daemon, "defaults")
	heartbeat := mappingValue(defaults, "heartbeat")
	if heartbeat == nil {
		return "", "", nil
	}
	if heartbeat.Kind != yaml.MappingNode {
		return "", "", errors.New("daemon.defaults.heartbeat must be a mapping")
	}
	every, body := mappingValue(heartbeat, "every"), mappingValue(heartbeat, "body")
	if every == nil || every.Kind != yaml.ScalarNode || body == nil || body.Kind != yaml.ScalarNode {
		return "", "", errors.New("heartbeat every and body must be strings")
	}
	duration, err := time.ParseDuration(every.Value)
	if err != nil || duration < time.Second {
		return "", "", errors.New("heartbeat every must be at least 1s")
	}
	if body.Value == "" || len(body.Value) > 60*1024 || !utf8.ValidString(body.Value) {
		return "", "", errors.New("heartbeat body must contain 1 to 61440 UTF-8 bytes")
	}
	return every.Value, body.Value, nil
}
