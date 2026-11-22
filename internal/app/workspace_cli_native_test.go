package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/i9wa4/tmux-a2a-postman/internal/cli"
	"github.com/i9wa4/tmux-a2a-postman/internal/workspace"
)

func TestWorkspaceCLIStatusShowsOnlyDirectInterfaceRelatives(t *testing.T) {
	f := newPublicApprovalFixture(t)
	childRoot := filepath.Join(f.home, "work", "child")
	grandRoot := filepath.Join(childRoot, "grandchild")
	for _, dir := range []string{childRoot, grandRoot} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	child := f.tmux("new-session", "-d", "-s", "child", "-c", childRoot, "-P", "-F", "#{pane_id}", "exec sleep 120")
	f.tmux("select-pane", "-t", child, "-T", "worker")
	grand := f.tmux("new-session", "-d", "-s", "grandchild", "-c", grandRoot, "-P", "-F", "#{pane_id}", "exec sleep 120")
	f.tmux("select-pane", "-t", grand, "-T", "worker")
	content := "---\ninterface_node: worker\n---\n## `edges`\n```mermaid\ngraph LR\nworker --- peer\n```\n## `worker`\nWorker\n## `peer`\nPeer\n"
	if err := os.WriteFile(f.config, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, pane := range []string{f.worker, child, grand} {
		if got, err := f.run(pane, "", "pop", "--config", f.config); err != nil || !got.Success {
			t.Fatal(got, err)
		}
	}
	status, err := f.run(f.worker, "", "get-status", "--config", f.config)
	if err != nil || len(status.Sessions) != 3 {
		t.Fatal(status, err)
	}
	own := findStatusPane(t, status.Sessions, f.worker)
	if own.Scope == nil || own.Scope.Interface != "worker" {
		t.Fatal(own)
	}
	childStatus := findStatusPane(t, status.Sessions, child)
	if childStatus.Workspace != "~/work/child" || childStatus.Status == nil || childStatus.Status.UnreadCount == nil {
		t.Fatal(childStatus)
	}
	failed, sendErr := f.run(f.worker, "must not publish", "send-heredoc", "--config", f.config, "--to", "missing:worker")
	if sendErr == nil || failed.Success {
		t.Fatal("unavailable directory peer was accepted", failed, sendErr)
	}
	nested, err := f.run(child, "", "get-status", "--config", f.config)
	if err != nil || len(nested.Sessions) != 3 || nested.AsOf != status.AsOf { // Failed send may publish another snapshot.
		if err != nil || len(nested.Sessions) != 3 {
			t.Fatal(nested, err)
		}
	}
	normal, err := f.run(f.peer, "", "get-status", "--config", f.config)
	if err != nil || normal.AsOf != nested.AsOf || len(normal.Sessions) != 3 {
		t.Fatal(normal, err)
	}
}

func TestDirectoryRouteUsesEachPeersRegisteredInterface(t *testing.T) {
	f := newPublicApprovalFixture(t)
	childRoot := filepath.Join(f.home, "work", "child")
	if err := os.MkdirAll(childRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	child := f.tmux("new-session", "-d", "-s", "child", "-c", childRoot, "-P", "-F", "#{pane_id}", "exec sleep 120")
	f.tmux("select-pane", "-t", child, "-T", "messenger")
	parentConfig := f.config
	childConfig := filepath.Join(f.home, "child-postman.md")
	for path, content := range map[string]string{
		parentConfig: "---\ninterface_node: worker\n---\n## `edges`\n```mermaid\ngraph LR\nworker --- peer\n```\n## `worker`\nWorker\n## `peer`\nPeer\n",
		childConfig:  "---\ninterface_node: messenger\n---\n## `edges`\n```mermaid\ngraph LR\nmessenger --- helper\n```\n## `messenger`\nMessenger\n## `helper`\nHelper\n",
	} {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if result, err := f.run(f.worker, "", "pop", "--config", parentConfig); err != nil || !result.Success {
		t.Fatal("parent registration", result, err)
	}
	if result, err := f.run(f.worker, "unregistered peer", "send-heredoc", "--config", parentConfig, "--to", "child:messenger"); err == nil || result.Success {
		t.Fatal("unregistered title became a route", result, err)
	}
	if result, err := f.run(child, "", "pop", "--config", childConfig); err != nil || !result.Success {
		t.Fatal("child registration", result, err)
	}
	if result, err := f.run(f.worker, "wrong role", "send-heredoc", "--config", parentConfig, "--to", "child:worker"); err == nil || result.Success {
		t.Fatal("caller-selected peer role became a route", result, err)
	}
	requestBody := "registered cross-config task\n"
	if result, err := f.run(f.worker, requestBody, "send-heredoc", "--config", parentConfig, "--to", "child:messenger"); err != nil || !result.Success {
		t.Fatal("cross-config task", result, err)
	}
	pop, err := f.run(child, "", "pop", "--config", childConfig)
	if err != nil || !pop.Success || pop.MarkdownPath == nil {
		t.Fatal("child POP", pop, err)
	}
	complete, err := os.ReadFile(*pop.MarkdownPath)
	if err != nil || !strings.Contains(string(complete), requestBody) {
		t.Fatal("complete child archive", err)
	}
	request := readProtocolArchive(t, *pop.MarkdownPath)
	if request.From != "worker" || request.To != "messenger" || request.FromSession != "work" || request.ToSession != "child" || request.Context == nil || !strings.Contains(request.Context.Markdown, "work:worker") || !strings.Contains(request.Context.Markdown, "Messenger") {
		t.Fatal("registered route or contact", request)
	}
	answerBody := "registered cross-config answer\n"
	if result, err := f.run(child, answerBody, "send-heredoc", "--config", childConfig, "--to", request.ID); err != nil || !result.Success {
		t.Fatal("exact child reply", result, err)
	}
	pop, err = f.run(f.worker, "", "pop", "--config", parentConfig)
	if err != nil || !pop.Success || pop.MarkdownPath == nil {
		t.Fatal("parent POP", pop, err)
	}
	complete, err = os.ReadFile(*pop.MarkdownPath)
	if err != nil || !strings.Contains(string(complete), answerBody) {
		t.Fatal("complete parent archive", err)
	}
	answer := readProtocolArchive(t, *pop.MarkdownPath)
	if len(answer.Replies) != 1 || answer.Replies[0].MessageID != request.ID || answer.From != "messenger" || answer.To != "worker" {
		t.Fatal("exact reply correlation", answer)
	}
}

func TestCrossDirectoryArchiveListsRecipientParentOtherChild(t *testing.T) {
	f := newPublicApprovalFixture(t)
	parentConfig := f.config
	parentPolicy := "---\ninterface_node: worker\n---\n## `edges`\n```mermaid\ngraph LR\nworker --- peer\n```\n## `worker`\nWorker\n## `peer`\nPeer\n"
	if err := os.WriteFile(parentConfig, []byte(parentPolicy), 0o600); err != nil {
		t.Fatal(err)
	}
	children := map[string]struct{ pane, config, role string }{}
	for _, item := range []struct{ session, role string }{{"alpha", "messenger"}, {"beta", "gateway"}} {
		root := filepath.Join(f.home, "work", item.session)
		if err := os.MkdirAll(root, 0o700); err != nil {
			t.Fatal(err)
		}
		pane := f.tmux("new-session", "-d", "-s", item.session, "-c", root, "-P", "-F", "#{pane_id}", "exec sleep 120")
		f.tmux("select-pane", "-t", pane, "-T", item.role)
		path := filepath.Join(f.home, item.session+"-postman.md")
		policy := "---\ninterface_node: " + item.role + "\n---\n## `edges`\n```mermaid\ngraph LR\n" + item.role + " --- helper\n```\n## `" + item.role + "`\nChild interface\n## `helper`\nHelper\n"
		if err := os.WriteFile(path, []byte(policy), 0o600); err != nil {
			t.Fatal(err)
		}
		children[item.session] = struct{ pane, config, role string }{pane, path, item.role}
	}
	for _, item := range []struct{ pane, config string }{{f.worker, parentConfig}, {children["alpha"].pane, children["alpha"].config}, {children["beta"].pane, children["beta"].config}} {
		if result, err := f.run(item.pane, "", "pop", "--config", item.config); err != nil || !result.Success {
			t.Fatal("interface registration", result, err)
		}
	}
	alpha := children["alpha"]
	if result, err := f.run(alpha.pane, "from alpha", "send-heredoc", "--config", alpha.config, "--to", "work:worker"); err != nil || !result.Success {
		t.Fatal("alpha to parent", result, err)
	}
	claimed, err := f.run(f.worker, "", "pop", "--config", parentConfig)
	if err != nil || !claimed.Success || claimed.MarkdownPath == nil {
		t.Fatal("parent POP", claimed, err)
	}
	archive := readProtocolArchive(t, *claimed.MarkdownPath)
	if archive.Context == nil {
		t.Fatal("missing recipient context", archive)
	}
	_, after, ok := strings.Cut(archive.Context.Markdown, "You can talk to:\n\n")
	if !ok {
		t.Fatal("missing contact section", archive.Context.Markdown)
	}
	contacts, _, ok := strings.Cut(after, "\n\nThese are configured routes")
	if !ok || !strings.Contains(contacts, "- alpha:messenger") || !strings.Contains(contacts, "- beta:gateway") || strings.Contains(contacts, "- work:worker") {
		t.Fatal("parent contacts were filtered through alpha's view", contacts)
	}
}

func TestCrossDirectoryArchiveKeepsRecipientContactsWithUnknownOffRouteSibling(t *testing.T) {
	f := newPublicApprovalFixture(t)
	parentConfig := f.config
	parentPolicy := "---\ninterface_node: worker\n---\n## `edges`\n```mermaid\ngraph LR\nworker --- peer\n```\n## `worker`\nWorker\n## `peer`\nPeer\n"
	if err := os.WriteFile(parentConfig, []byte(parentPolicy), 0o600); err != nil {
		t.Fatal(err)
	}
	type child struct{ pane, config, role, root string }
	children := map[string]child{}
	for _, item := range []struct{ session, role string }{{"alpha", "messenger"}, {"beta", "gateway"}} {
		root := filepath.Join(f.home, "work", item.session)
		if err := os.MkdirAll(root, 0o700); err != nil {
			t.Fatal(err)
		}
		pane := f.tmux("new-session", "-d", "-s", item.session, "-c", root, "-P", "-F", "#{pane_id}", "exec sleep 120")
		f.tmux("select-pane", "-t", pane, "-T", item.role)
		path := filepath.Join(f.home, item.session+"-postman.md")
		policy := "---\ninterface_node: " + item.role + "\n---\n## `edges`\n```mermaid\ngraph LR\n" + item.role + " --- helper\n```\n## `" + item.role + "`\nChild interface\n## `helper`\nHelper\n"
		if err := os.WriteFile(path, []byte(policy), 0o600); err != nil {
			t.Fatal(err)
		}
		children[item.session] = child{pane, path, item.role, root}
	}
	for _, item := range []child{{pane: f.worker, config: parentConfig}, children["alpha"], children["beta"]} {
		if result, err := f.run(item.pane, "", "pop", "--config", item.config); err != nil || !result.Success {
			t.Fatal("interface registration", result, err)
		}
	}
	beta := children["beta"]
	canonical, err := workspace.CanonicalRoot(beta.root, f.home)
	if err != nil {
		t.Fatal(err)
	}
	name, err := workspace.Name(canonical)
	if err != nil {
		t.Fatal(err)
	}
	statusConfig := filepath.Join(f.stateRoot(), name, "runtime", "status-config-p"+strings.TrimPrefix(beta.pane, "%")+".json")
	if err := os.Remove(statusConfig); err != nil {
		t.Fatal(err)
	}
	alpha := children["alpha"]
	if result, err := f.run(f.worker, "task", "send-heredoc", "--config", parentConfig, "--to", "alpha:messenger"); err != nil || !result.Success {
		t.Fatal("parent to alpha", result, err)
	}
	claimed, err := f.run(alpha.pane, "", "pop", "--config", alpha.config)
	if err != nil || !claimed.Success || claimed.MarkdownPath == nil {
		t.Fatal("alpha POP", claimed, err)
	}
	request := readProtocolArchive(t, *claimed.MarkdownPath)
	if request.Context == nil || !strings.Contains(request.Context.Markdown, "- work:worker") {
		t.Fatal("alpha archive lost its verified parent contact", request.Context)
	}
	if result, err := f.run(alpha.pane, "answer", "send-heredoc", "--config", alpha.config, "--to", request.ID); err != nil || !result.Success {
		t.Fatal("alpha exact reply", result, err)
	}
	claimed, err = f.run(f.worker, "", "pop", "--config", parentConfig)
	if err != nil || !claimed.Success || claimed.MarkdownPath == nil {
		t.Fatal("parent POP", claimed, err)
	}
	answer := readProtocolArchive(t, *claimed.MarkdownPath)
	if len(answer.Replies) != 1 || answer.Replies[0].MessageID != request.ID || answer.Context == nil ||
		!strings.Contains(answer.Context.Markdown, "- alpha:messenger") {
		t.Fatal("exact reply lost correlation or addressed child contact", answer.Context, answer)
	}
}

func TestCrossDirectoryArchiveRejectsIncompleteParentContacts(t *testing.T) {
	f := newPublicApprovalFixture(t)
	parentPolicy := "---\ninterface_node: worker\n---\n## `edges`\n```mermaid\ngraph LR\nworker --- peer\n```\n## `worker`\nWorker\n## `peer`\nPeer\n"
	if err := os.WriteFile(f.config, []byte(parentPolicy), 0o600); err != nil {
		t.Fatal(err)
	}
	type child struct{ pane, config string }
	children := map[string]child{}
	for _, name := range []string{"alpha", "beta"} {
		root := filepath.Join(f.home, "work", name)
		if err := os.MkdirAll(root, 0o700); err != nil {
			t.Fatal(err)
		}
		pane := f.tmux("new-session", "-d", "-s", name, "-c", root, "-P", "-F", "#{pane_id}", "exec sleep 120")
		f.tmux("select-pane", "-t", pane, "-T", "messenger")
		configPath := filepath.Join(f.home, name+"-postman.md")
		policy := "---\ninterface_node: messenger\n---\n## `edges`\n```mermaid\ngraph LR\nmessenger --- helper\n```\n## `messenger`\nMessenger\n## `helper`\nHelper\n"
		if err := os.WriteFile(configPath, []byte(policy), 0o600); err != nil {
			t.Fatal(err)
		}
		children[name] = child{pane, configPath}
	}
	for _, item := range []child{{f.worker, f.config}, children["alpha"], children["beta"]} {
		if result, err := f.run(item.pane, "", "pop", "--config", item.config); err != nil || !result.Success {
			t.Fatal("interface registration", result, err)
		}
	}
	beta := children["beta"]
	betaRoot, err := workspace.CanonicalRoot(filepath.Join(f.home, "work", "beta"), f.home)
	if err != nil {
		t.Fatal(err)
	}
	betaName, err := workspace.Name(betaRoot)
	if err != nil {
		t.Fatal(err)
	}
	statusConfig := filepath.Join(f.stateRoot(), betaName, "runtime", "status-config-p"+strings.TrimPrefix(beta.pane, "%")+".json")
	raw, err := os.ReadFile(statusConfig)
	if err != nil {
		t.Fatal(err)
	}
	var record map[string]any
	if err := json.Unmarshal(raw, &record); err != nil {
		t.Fatal(err)
	}
	record["fingerprint"] = "sha256:stale"
	raw, err = json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(statusConfig, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	alpha := children["alpha"]
	result, err := f.run(alpha.pane, "incomplete parent contacts", "send-heredoc", "--config", alpha.config, "--to", "work:worker")
	if err == nil || result.Success || !strings.Contains(result.Reason, "complete recipient directory contacts") {
		t.Fatal("unverified sibling produced incomplete parent contacts", result, err)
	}
	if result, err := f.run(f.worker, "", "pop", "--config", f.config); err != nil || !result.Success || result.MarkdownPath != nil {
		t.Fatal("incomplete recipient context was published", result, err)
	}
	if result, err := f.run(f.peer, "incomplete local contacts", "send-heredoc", "--config", f.config, "--to", "worker"); err == nil || result.Success || !strings.Contains(result.Reason, "complete recipient directory contacts") {
		t.Fatal("local send published incomplete parent contacts", result, err)
	}
	if result, err := f.run(beta.pane, "", "pop", "--config", beta.config); err != nil || !result.Success {
		t.Fatal("beta re-registration", result, err)
	}
	if result, err := f.run(alpha.pane, "complete parent contacts", "send-heredoc", "--config", alpha.config, "--to", "work:worker"); err != nil || !result.Success {
		t.Fatal("alpha to re-registered parent", result, err)
	}
	claimed, err := f.run(f.worker, "", "pop", "--config", f.config)
	if err != nil || !claimed.Success || claimed.MarkdownPath == nil {
		t.Fatal("parent POP", claimed, err)
	}
	complete, err := os.ReadFile(*claimed.MarkdownPath)
	if err != nil || !strings.Contains(string(complete), "complete parent contacts") {
		t.Fatal("complete parent archive", err)
	}
	request := readProtocolArchive(t, *claimed.MarkdownPath)
	if request.Context == nil || !strings.Contains(request.Context.Markdown, "- alpha:messenger") || !strings.Contains(request.Context.Markdown, "- beta:messenger") {
		t.Fatal("parent archive omitted a verified child", request.Context)
	}
	if result, err := f.run(f.worker, "complete answer", "send-heredoc", "--config", f.config, "--to", request.ID); err != nil || !result.Success {
		t.Fatal("parent exact reply", result, err)
	}
	claimed, err = f.run(alpha.pane, "", "pop", "--config", alpha.config)
	if err != nil || !claimed.Success || claimed.MarkdownPath == nil {
		t.Fatal("alpha reply POP", claimed, err)
	}
	complete, err = os.ReadFile(*claimed.MarkdownPath)
	if err != nil || !strings.Contains(string(complete), "complete answer") {
		t.Fatal("complete alpha reply archive", err)
	}
	answer := readProtocolArchive(t, *claimed.MarkdownPath)
	if len(answer.Replies) != 1 || answer.Replies[0].MessageID != request.ID || answer.Context == nil || !strings.Contains(answer.Context.Markdown, "- work:worker") || strings.Contains(answer.Context.Markdown, "beta:messenger") {
		t.Fatal("child lost its single parent or reply correlation", answer)
	}
	grandRoot := filepath.Join(f.home, "work", "beta", "gamma")
	if err := os.MkdirAll(grandRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	grandPane := f.tmux("new-session", "-d", "-s", "gamma", "-c", grandRoot, "-P", "-F", "#{pane_id}", "exec sleep 120")
	f.tmux("select-pane", "-t", grandPane, "-T", "messenger")
	grandConfig := filepath.Join(f.home, "gamma-postman.md")
	grandPolicy := "---\ninterface_node: messenger\n---\n## `edges`\n```mermaid\ngraph LR\nmessenger --- helper\n```\n## `messenger`\nMessenger\n## `helper`\nHelper\n"
	if err := os.WriteFile(grandConfig, []byte(grandPolicy), 0o600); err != nil {
		t.Fatal(err)
	}
	if result, err := f.run(grandPane, "", "pop", "--config", grandConfig); err != nil || !result.Success {
		t.Fatal("grandchild registration", result, err)
	}
	if err := os.Remove(statusConfig); err != nil {
		t.Fatal(err)
	}
	if result, err := f.run(alpha.pane, "missing intermediate", "send-heredoc", "--config", alpha.config, "--to", "work:worker"); err != nil || !result.Success {
		t.Fatal("alpha to parent with missing beta provenance", result, err)
	}
	claimed, err = f.run(f.worker, "", "pop", "--config", f.config)
	if err != nil || !claimed.Success || claimed.MarkdownPath == nil {
		t.Fatal("parent POP with missing intermediate", claimed, err)
	}
	frontier := readProtocolArchive(t, *claimed.MarkdownPath)
	if frontier.Context == nil || !strings.Contains(frontier.Context.Markdown, "- alpha:messenger") || strings.Contains(frontier.Context.Markdown, "- beta:messenger") || strings.Contains(frontier.Context.Markdown, "- gamma:messenger") {
		t.Fatal("missing intermediate exposed a grandchild as direct contact", frontier.Context)
	}
	if result, err := f.run(f.worker, "blocked shortcut", "send-heredoc", "--config", f.config, "--to", "gamma:messenger"); err == nil || result.Success {
		t.Fatal("missing intermediate admitted grandchild shortcut", result, err)
	}
	grandHelper := f.tmux("new-window", "-d", "-t", "gamma", "-c", grandRoot, "-P", "-F", "#{pane_id}", "exec sleep 120")
	f.tmux("select-pane", "-t", grandHelper, "-T", "helper")
	if result, err := f.run(grandHelper, "missing immediate parent", "send-heredoc", "--config", grandConfig, "--to", "messenger"); err != nil || !result.Success {
		t.Fatal("grandchild local send with unavailable parent", result, err)
	}
	claimed, err = f.run(grandPane, "", "pop", "--config", grandConfig)
	if err != nil || !claimed.Success || claimed.MarkdownPath == nil {
		t.Fatal("grandchild POP with unavailable parent", claimed, err)
	}
	grandArchive := readProtocolArchive(t, *claimed.MarkdownPath)
	if grandArchive.Context == nil || strings.Contains(grandArchive.Context.Markdown, "- work:worker") || strings.Contains(grandArchive.Context.Markdown, "- beta:messenger") {
		t.Fatal("missing immediate parent promoted a grandparent contact", grandArchive.Context)
	}
	if result, err := f.run(beta.pane, "", "pop", "--config", beta.config); err != nil || !result.Success {
		t.Fatal("beta re-registration before stale grandchild", result, err)
	}
	if result, err := f.run(f.worker, "recovered beta task", "send-heredoc", "--config", f.config, "--to", "beta:messenger"); err != nil || !result.Success {
		t.Fatal("parent to recovered direct child beta", result, err)
	}
	claimed, err = f.run(beta.pane, "", "pop", "--config", beta.config)
	if err != nil || !claimed.Success || claimed.MarkdownPath == nil {
		t.Fatal("beta task POP", claimed, err)
	}
	complete, err = os.ReadFile(*claimed.MarkdownPath)
	if err != nil || !strings.Contains(string(complete), "recovered beta task") {
		t.Fatal("complete beta task archive", err)
	}
	betaRequest := readProtocolArchive(t, *claimed.MarkdownPath)
	if betaRequest.Context == nil || !strings.Contains(betaRequest.Context.Markdown, "- work:worker") || !strings.Contains(betaRequest.Context.Markdown, "- gamma:messenger") || strings.Contains(betaRequest.Context.Markdown, "- alpha:messenger") {
		t.Fatal("beta did not see its immediate parent and direct child", betaRequest.Context)
	}
	if result, err := f.run(beta.pane, "recovered beta answer", "send-heredoc", "--config", beta.config, "--to", betaRequest.ID); err != nil || !result.Success {
		t.Fatal("beta exact reply", result, err)
	}
	claimed, err = f.run(f.worker, "", "pop", "--config", f.config)
	if err != nil || !claimed.Success || claimed.MarkdownPath == nil {
		t.Fatal("parent beta-answer POP", claimed, err)
	}
	complete, err = os.ReadFile(*claimed.MarkdownPath)
	if err != nil || !strings.Contains(string(complete), "recovered beta answer") {
		t.Fatal("complete parent beta-answer archive", err)
	}
	betaAnswer := readProtocolArchive(t, *claimed.MarkdownPath)
	if len(betaAnswer.Replies) != 1 || betaAnswer.Replies[0].MessageID != betaRequest.ID || betaAnswer.Context == nil || !strings.Contains(betaAnswer.Context.Markdown, "- alpha:messenger") || !strings.Contains(betaAnswer.Context.Markdown, "- beta:messenger") || strings.Contains(betaAnswer.Context.Markdown, "- gamma:messenger") {
		t.Fatal("beta reply lost correlation or parent direct-child frontier", betaAnswer)
	}
	grandCanonical, err := workspace.CanonicalRoot(grandRoot, f.home)
	if err != nil {
		t.Fatal(err)
	}
	grandName, err := workspace.Name(grandCanonical)
	if err != nil {
		t.Fatal(err)
	}
	grandStatus := filepath.Join(f.stateRoot(), grandName, "runtime", "status-config-p"+strings.TrimPrefix(grandPane, "%")+".json")
	raw, err = os.ReadFile(grandStatus)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &record); err != nil {
		t.Fatal(err)
	}
	record["fingerprint"] = "sha256:stale"
	raw, err = json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(grandStatus, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if result, err := f.run(f.peer, "stale grandchild is not direct", "send-heredoc", "--config", f.config, "--to", "worker"); err != nil || !result.Success {
		t.Fatal("parent local send was vetoed by stale grandchild", result, err)
	}
	claimed, err = f.run(f.worker, "", "pop", "--config", f.config)
	if err != nil || !claimed.Success || claimed.MarkdownPath == nil {
		t.Fatal("parent POP with stale grandchild", claimed, err)
	}
	frontier = readProtocolArchive(t, *claimed.MarkdownPath)
	if frontier.Context == nil || !strings.Contains(frontier.Context.Markdown, "- alpha:messenger") || !strings.Contains(frontier.Context.Markdown, "- beta:messenger") || strings.Contains(frontier.Context.Markdown, "- gamma:messenger") {
		t.Fatal("stale grandchild vetoed or entered parent direct contacts", frontier.Context)
	}
}

func TestInterfaceContactDiscoveryTimeoutReportsRecovery(t *testing.T) {
	f := newPublicApprovalFixture(t)
	policy := "---\ninterface_node: worker\n---\n## `edges`\n```mermaid\ngraph LR\nworker --- peer\n```\n## `worker`\nWorker\n## `peer`\nPeer\n"
	if err := os.WriteFile(f.config, []byte(policy), 0o600); err != nil {
		t.Fatal(err)
	}
	if result, err := f.run(f.worker, "", "pop", "--config", f.config); err != nil || !result.Success {
		t.Fatal("parent registration", result, err)
	}
	realTmux, err := exec.LookPath("tmux")
	if err != nil {
		t.Fatal(err)
	}
	shimDir := t.TempDir()
	shim := "#!/bin/sh\nfor arg do\n if [ \"$arg\" = list-sessions ] && [ ! -e \"$POSTMAN_TEST_MARKER\" ]; then\n  : > \"$POSTMAN_TEST_MARKER\"\n  sleep 3\n fi\ndone\nexec \"$POSTMAN_REAL_TMUX\" \"$@\"\n"
	if err := os.WriteFile(filepath.Join(shimDir, "tmux"), []byte(shim), 0o700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := f.child(ctx, f.peer, "send-heredoc", "--config", f.config, "--to", "worker")
	cmd.Env = append(cmd.Env,
		"PATH="+shimDir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"POSTMAN_TEST_MARKER="+filepath.Join(shimDir, "delayed"),
		"POSTMAN_REAL_TMUX="+realTmux)
	cmd.Stdin = strings.NewReader("timeout contacts")
	raw, err := cmd.Output()
	if ctx.Err() != nil {
		t.Fatal("CLI timed out before reporting contact recovery", ctx.Err())
	}
	var result protocolOutput
	if json.Unmarshal(raw, &result) != nil || err == nil || result.Success || !strings.Contains(result.Reason, "recipient directory contacts timed out") || strings.Contains(result.Reason, "registrations are current") {
		t.Fatal("timeout was reported as registration failure", string(raw), err)
	}
	if claimed, err := f.run(f.worker, "", "pop", "--config", f.config); err != nil || !claimed.Success || claimed.MarkdownPath != nil {
		t.Fatal("timeout published incomplete contacts", claimed, err)
	}
}

func TestExternalChannelIsBoundToExactChildInterface(t *testing.T) {
	f := newPublicApprovalFixture(t)
	childRoot := filepath.Join(f.home, "work", "child")
	if err := os.MkdirAll(childRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	child := f.tmux("new-session", "-d", "-s", "child", "-c", childRoot, "-P", "-F", "#{pane_id}", "exec sleep 120")
	f.tmux("select-pane", "-t", child, "-T", "messenger")
	root := filepath.Join(f.home, "child-external")
	configPath := filepath.Join(f.home, "child-external.md")
	content := "---\ninterface_node: messenger\nexternal_mailbox_root: " + root + "\nexternal_workspace: ~/work/child\nexternal_session: child\n---\n## `edges`\n```mermaid\ngraph LR\nmessenger --- helper\n```\n## `messenger`\nMessenger\n## `helper`\nHelper\n"
	if err := os.WriteFile(configPath, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	if result, err := f.run(f.worker, "", "channel-sync", "--config", configPath); err == nil || result.Success {
		t.Fatal("parent accessed child binding", result, err)
	}
	if result, err := f.run(child, "", "channel-sync", "--config", configPath); err != nil || !result.Success {
		t.Fatal("configured child binding", result, err)
	}
	if _, err := os.Stat(filepath.Join(root, "binding.json")); err != nil {
		t.Fatal("child binding missing", err)
	}
}

func TestWorkspaceCLILocalArchiveListsNearestDirectoryContacts(t *testing.T) {
	f := newPublicApprovalFixture(t)
	childRoot := filepath.Join(f.home, "work", "child")
	var childPane string
	directoryPanes := []string{f.worker}
	for _, item := range []struct{ name, root string }{
		{"child", childRoot},
		{"grandchild", filepath.Join(childRoot, "grandchild")},
		{"sibling", filepath.Join(f.home, "work", "sibling")},
		{"prefix", filepath.Join(f.home, "work", "childish")},
	} {
		if err := os.MkdirAll(item.root, 0o700); err != nil {
			t.Fatal(err)
		}
		pane := f.tmux("new-session", "-d", "-s", item.name, "-c", item.root, "-P", "-F", "#{pane_id}", "exec sleep 120")
		f.tmux("select-pane", "-t", pane, "-T", "worker")
		directoryPanes = append(directoryPanes, pane)
		if item.name == "child" {
			childPane = pane
		}
	}
	childPeer := f.tmux("new-window", "-d", "-t", "child", "-c", childRoot, "-P", "-F", "#{pane_id}", "exec sleep 120")
	f.tmux("select-pane", "-t", childPeer, "-T", "peer")
	content := "---\ninterface_node: worker\n---\n## `edges`\n```mermaid\ngraph LR\nworker --- peer\n```\n## `worker`\nWorker\n## `peer`\nPeer\n"
	if err := os.WriteFile(f.config, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, pane := range directoryPanes {
		if result, err := f.run(pane, "", "pop", "--config", f.config); err != nil || !result.Success {
			t.Fatal("register directory interface", result, err)
		}
	}
	if sent, err := f.run(childPeer, "local directory contacts", "send-heredoc", "--config", f.config, "--to", "worker"); err != nil || !sent.Success {
		t.Fatal(sent, err)
	}
	claimed, err := f.run(childPane, "", "pop", "--config", f.config)
	if err != nil || !claimed.Success || claimed.MarkdownPath == nil {
		t.Fatal(claimed, err)
	}
	archive := readProtocolArchive(t, *claimed.MarkdownPath)
	if archive.Body != "local directory contacts" || archive.Context == nil {
		t.Fatal("local message lost body or generated context", archive)
	}
	_, after, ok := strings.Cut(archive.Context.Markdown, "You can talk to:\n\n")
	if !ok {
		t.Fatal("missing contact section", archive.Context.Markdown)
	}
	contacts, _, ok := strings.Cut(after, "\n\nThese are configured routes")
	if !ok || !strings.Contains(contacts, "- work:worker (parent)") || !strings.Contains(contacts, "- grandchild:worker (child)") || !strings.Contains(contacts, "- peer") || strings.Contains(contacts, "sibling:worker") || strings.Contains(contacts, "prefix:worker") {
		t.Fatal("local archive contacts did not match nearest live routes", contacts)
	}
}

func TestWorkspaceCLIRespawnBeforeSendCannotAdoptReplacement(t *testing.T) {
	f := newPublicApprovalFixture(t)
	realTmux, err := exec.LookPath("tmux")
	if err != nil {
		t.Fatal(err)
	}
	proxyDir := t.TempDir()
	counter := filepath.Join(proxyDir, "counter")
	proxy := fmt.Sprintf(`#!/bin/sh
"%s" "$@"
result=$?
case "$*" in
 *'#{socket_path}'*)
 count=0
 if [ -f "%s" ]; then count=$(cat "%s"); fi
 count=$((count+1)); printf '%%s' "$count" > "%s"
 if [ "$count" -eq 2 ]; then
  "%s" -S "%s" respawn-pane -k -t "%s" 'exec sleep 120' || exit 98
  "%s" -S "%s" select-pane -t "%s" -T worker || exit 98
 fi ;;
esac
exit "$result"
`, realTmux, counter, counter, counter, realTmux, f.socket, f.worker, realTmux, f.socket, f.worker)
	if err := os.WriteFile(filepath.Join(proxyDir, "tmux"), []byte(proxy), 0o700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	cmd := f.child(ctx, f.worker, "send-heredoc", "--config", f.config, "--to", "peer")
	cmd.Env = append(cmd.Env, "PATH="+proxyDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	cmd.Stdin = strings.NewReader("old caller must not publish")
	raw, err := cmd.Output()
	if err == nil {
		t.Fatal("old invocation adopted replacement", string(raw))
	}
	var result protocolOutput
	if json.Unmarshal(raw, &result) != nil || result.Success {
		t.Fatal("invalid failure result", string(raw))
	}
	if _, err := os.Stat(f.stateRoot()); !os.IsNotExist(err) {
		t.Fatal("stale invocation initialized replacement state", err)
	}
}

// The proxy changes only an owned pane, after the final native observation in
// the CLI's initial Snapshot. Thus the command starts with a worker snapshot
// but sees an alt role when it routes or reaches the mail lock.
func TestWorkspaceCLIRetitleBeforeSendFailsClosed(t *testing.T) {
	realTmux, err := exec.LookPath("tmux")
	if err != nil {
		t.Skip("tmux unavailable")
	}
	for _, tc := range []struct{ name, to string }{{"local", "local"}, {"cross", "child:worker"}} {
		t.Run(tc.name, func(t *testing.T) {
			dir, err := os.MkdirTemp("/tmp", "pm937-retitle-")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.RemoveAll(dir) })
			socket := filepath.Join(dir, "s")
			runTmux := func(args ...string) string {
				t.Helper()
				ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
				defer cancel()
				out, err := exec.CommandContext(ctx, realTmux, append([]string{"-S", socket}, args...)...).CombinedOutput()
				if err != nil {
					t.Fatalf("owned tmux %v: %v: %s", args, err, out)
				}
				return strings.TrimSpace(string(out))
			}
			home := t.TempDir()
			for _, name := range []string{"parent", "parent/child"} {
				if err := os.MkdirAll(filepath.Join(home, name), 0o700); err != nil {
					t.Fatal(err)
				}
			}
			parent := runTmux("-f", "/dev/null", "new-session", "-d", "-s", "parent", "-c", filepath.Join(home, "parent"), "-P", "-F", "#{pane_id}", "exec sleep 120")
			t.Cleanup(func() {
				ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				defer cancel()
				_ = exec.CommandContext(ctx, realTmux, "-S", socket, "kill-server").Run()
			})
			runTmux("select-pane", "-t", parent, "-T", "worker")
			local := runTmux("new-window", "-d", "-t", "parent", "-c", filepath.Join(home, "parent"), "-P", "-F", "#{pane_id}", "exec sleep 120")
			runTmux("select-pane", "-t", local, "-T", "local")
			child := runTmux("new-session", "-d", "-s", "child", "-c", filepath.Join(home, "parent", "child"), "-P", "-F", "#{pane_id}", "exec sleep 120")
			runTmux("select-pane", "-t", child, "-T", "worker")

			config := filepath.Join(t.TempDir(), "postman.md")
			content := "---\ninterface_node: worker\n---\n## `edges`\n```mermaid\ngraph LR\nworker --- local\nalt --- worker\n```\n## `worker`\nWorker\n## `local`\nLocal\n## `alt`\nAlt\n"
			if err := os.WriteFile(config, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			// The proxy forwards exclusively to this fixture's private socket.
			// Its second long display is the initial Snapshot's final observation.
			proxyDir := t.TempDir()
			counter := filepath.Join(proxyDir, "count")
			proxy := fmt.Sprintf(`#!/bin/sh
case "$1:$2:$3" in
  -u:-S:%s) ;;
  *) exit 97 ;;
esac
"%s" "$@"
status=$?
case "$*" in
  *'#{socket_path}'*)
    count=0
    if [ -f "%s" ]; then count=$(cat "%s"); fi
    count=$((count + 1))
    printf '%%s' "$count" > "%s"
    if [ "$count" -eq 2 ]; then
      "%s" -S "%s" select-pane -t "%s" -T alt || exit 98
    fi
    ;;
esac
exit "$status"
`, socket, realTmux, counter, counter, counter, realTmux, socket, parent)
			if err := os.WriteFile(filepath.Join(proxyDir, "tmux"), []byte(proxy), 0o755); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", proxyDir+string(os.PathListSeparator)+os.Getenv("PATH"))
			stateHome := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("XDG_STATE_HOME", stateHome)
			t.Setenv("TMUX", socket+",0,0")
			t.Setenv("TMUX_PANE", parent)
			readStdin, writeStdin, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			if _, err := writeStdin.WriteString("must not publish\n"); err != nil {
				t.Fatal(err)
			}
			_ = writeStdin.Close()
			previousStdin := os.Stdin
			os.Stdin = readStdin
			t.Cleanup(func() { os.Stdin = previousStdin; _ = readStdin.Close() })
			readOut, writeOut, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			previousOut := os.Stdout
			os.Stdout = writeOut
			sendErr := cli.RunWorkspaceSendHeredoc([]string{"--config", config, "--to", tc.to})
			os.Stdout = previousOut
			_ = writeOut.Close()
			out, readErr := io.ReadAll(readOut)
			_ = readOut.Close()
			if sendErr == nil || readErr != nil {
				t.Fatalf("retitled send: %v, output read: %v", sendErr, readErr)
			}
			var result protocolOutput
			if err := json.Unmarshal(out, &result); err != nil || result.Success || result.Self.UnreadCount != nil {
				t.Fatalf("retitle leaked successful send: %+v decode=%v raw=%s", result, err, out)
			}

			if title := runTmux("display-message", "-p", "-t", parent, "#{pane_title}"); title != "alt" {
				count, _ := os.ReadFile(counter)
				t.Fatalf("controlled retitle did not occur: title=%q, count=%q, output=%s, error=%v", title, count, out, sendErr)
			}
			root, err := workspace.DefaultStateRoot(home, stateHome)
			if err != nil {
				t.Fatal(err)
			}
			err = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
				if err != nil {
					return err
				}
				if !d.IsDir() && strings.HasSuffix(path, ".md") &&
					(strings.Contains(path, string(filepath.Separator)+"inbox"+string(filepath.Separator)) || strings.Contains(path, string(filepath.Separator)+"sent"+string(filepath.Separator))) {
					t.Errorf("retitled sender wrote mail: %s", path)
				}
				return nil
			})
			if err != nil && !errors.Is(err, os.ErrNotExist) {
				t.Fatal(err)
			}
		})
	}
}
