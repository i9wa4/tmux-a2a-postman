package workspace

import (
	"context"
	"encoding/json"
	"fmt"
)

// initialize performs the required per-connection Codex app-server handshake.
func (s *codexSocket) initialize(ctx context.Context) error {
	_, _, err := s.call(ctx, "initialize", map[string]any{
		"clientInfo": map[string]string{
			"name":    "tmux_a2a_postman",
			"title":   "tmux-a2a-postman",
			"version": "0.1.0",
		},
	})
	if err != nil {
		return err
	}
	return s.notify(ctx, "initialized", map[string]any{})
}

func (s *codexSocket) notify(ctx context.Context, method string, params any) error {
	if err := s.conn.SetDeadline(deadlineFromContext(ctx)); err != nil {
		return err
	}
	payload, err := json.Marshal(struct {
		Method string `json:"method"`
		Params any    `json:"params"`
	}{method, params})
	if err != nil {
		return err
	}
	return s.writeFrame(1, payload)
}

// startThread creates a thread on an already initialized recipient-owned
// app-server. The caller must persist its identity before starting a turn.
func (s *codexSocket) startThread(ctx context.Context, cwd string) (string, error) {
	if cwd == "" {
		return "", ErrUnknown
	}
	result, _, err := s.call(ctx, "thread/start", map[string]any{
		"cwd":         cwd,
		"serviceName": "tmux_a2a_postman",
	})
	if err != nil {
		return "", err
	}
	var response struct {
		Thread struct {
			ID string `json:"id"`
		} `json:"thread"`
	}
	if err := json.Unmarshal(result, &response); err != nil || response.Thread.ID == "" {
		return "", fmt.Errorf("%w: app-server thread/start returned no thread ID", ErrUnknown)
	}
	return response.Thread.ID, nil
}

// startBarePopTurn wakes an already recorded thread. The durable mail ID
// belongs in the caller's reservation record, never as an argument to pop.
func (s *codexSocket) startBarePopTurn(ctx context.Context, threadID, cwd string) (string, error) {
	if threadID == "" || cwd == "" {
		return "", ErrUnknown
	}
	result, _, err := s.call(ctx, "turn/start", map[string]any{
		"threadId": threadID,
		"cwd":      cwd,
		"input": []map[string]string{{
			"type": "text",
			"text": "tmux-a2a-postman pop",
		}},
	})
	if err != nil {
		return "", err
	}
	var response struct {
		Turn struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		} `json:"turn"`
	}
	if err := json.Unmarshal(result, &response); err != nil || response.Turn.ID == "" || response.Turn.Status != "inProgress" {
		return "", fmt.Errorf("%w: app-server turn/start returned no active turn", ErrUnknown)
	}
	return response.Turn.ID, nil
}
