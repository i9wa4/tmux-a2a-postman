# Native Codex notification verification

## 1. Scope and setup

Recorded 2026-10-31 on Linux with real tmux 3.5a and Codex CLI 0.163.0.
The model is a deterministic local Responses API fixture, not a real AI model.
The actual Codex TUI and native app-server queue execute the recipient tools.
No model authentication or paid requests are required.

Launch settings follow the Codex roles in i9wa4/dotfiles vde-layout:
`--yolo --no-daemon --no-alt-screen`, stable role pane titles, separate role
conversations. Only fixture HOME, CODEX_HOME, tmux socket and state are used.
No user configuration or unrelated tmux server is changed. The initial prompt
registers the recipient from its real CODEX_THREAD_ID and supplies a context
nonce; subsequent notifications must retain that conversation and nonce.

## 2. Before and after

The unmodified main binary at `8bc2780` commits the initial mail but reports
`notice.status=unavailable`. After twelve seconds, longer than Codex's native
external queue check, no recipient claims or replies exist and the inbox copy
remains unread. The controller never supplies POP or reply actions.

With the native adapter, the initial send returns in 0.257 seconds with a
correlated queue receipt. Four notification-driven claims complete. Original
thread IDs remain unchanged:

- orchestrator: `01a192d2-9f9e-7093-824c-a9bfc2cabdff`
- worker: `01a192d2-a32b-7600-b84b-708cec269bc2`

Mail chain, each claimed and fully read by the receiving Codex tool:

| Recipient    | Mail ID                            | Complete archive SHA256                                          |
| ------------ | ---------------------------------- | ---------------------------------------------------------------- |
| worker       | m-3e56ae3e1ef6177562c22c43058f0f95 | dc63d51764d9959a36fe59e338580b6866482b8643c4bb8f75395f852cd61404 |
| orchestrator | m-b4198d80158009ab1e95c4359c6ecadd | 83e14312c2f97d2b0a4f2495ba3030ef32d41896884ba76a37ca6a362e766f3f |
| worker       | m-82fbc71671d57cad6ef04d9b8f564ced | 01e1184814faa565b3521faea9085c5d753480948c4ea16562cece4656165199 |
| orchestrator | m-8c57497ed80f84f6d2e352630c6395d8 | 28d784fb9f70a1a13bd0c902de90f409b5388d3a87af9ef4aeda267b95f2fb77 |

Every reply fills the preceding message's exact input-request ID. Each full-file
hash matches the actual final read archive. The recipient's unsubmitted draft
remains in its actual Codex composer and is never submitted.

## 3. Regression coverage

All eight real-Codex cases passed: rally, busy recipient, two concurrent
independent requests, parent-to-child, child-to-parent, approval, rejection,
and disabled recipient registration. Busy sends return before recipient work;
rejected commands never create their execution marker. Disabled registration
preserves unread mail with zero recipient actions. Go fault tests separately
cover ambiguous responses, timeout, correlation mismatch, stale identity,
concurrent same-message attempts, no replay, no lock held across runtime RPC,
and reaping short-lived helpers. The targeted Go race check passes.

Reproduce with `nix run .#codex-notification-smoke -- --case all`.
`nix flake check` runs the suite using its pinned Codex 0.162.1. See
[Contributing](../CONTRIBUTING.md) for individual cases, machine-readable
reports, the old-main negative control and optional live-model execution.
The existing [Bash smoke](952-smoke.md) concerns the optional shell adapter;
it is not evidence of activation of an existing Codex conversation.

## 4. Limits

Queue acceptance alone is not POP, understanding or task completion. Codex's
native watcher currently checks every ten seconds and defers busy threads.
Register again after changing or restarting conversations. Actual paid-model
reasoning and Claude TUI activation are not established by this test. The
broader pane-capture attribution work in #937 remains separate.
