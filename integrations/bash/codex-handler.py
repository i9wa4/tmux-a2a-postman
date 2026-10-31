#!/usr/bin/env python3
"""One foreground Codex turn per notification; stdin is POP result JSON."""
import json
import os
import pathlib
import subprocess
import sys

result = json.load(sys.stdin)
if result["status"] == "empty":
    sys.exit(0)
if result["status"] != "message":
    raise RuntimeError("POP did not return a message")
archive = pathlib.Path(result["markdown_absolute_path"])
markdown = archive.read_text()  # Complete archive through EOF before handling.
prompt = (
    "Handle this Postman mail. Follow the workspace AGENTS.md and installed "
    "postman-usage/postman-send-message skills. The complete archive follows. "
    "Use POSTMAN_BIN for CLI calls and POSTMAN_CONFIG as --postman-md. "
    "If needs_reply is true, send a reply to the original sender, with exact "
    "--reply-to id and --fills-input-request-id input_request_id from the "
    "envelope. For cross-session mail address from_session:from. "
    "For command approval, reply with version=1, the unchanged command, and "
    "decision=approved or rejected only after reviewing it. Do not poll.\n\n"
    + markdown
)
# This is a fresh noninteractive recipient turn; it never consumes Readline.
# Install/configure Codex normally. Its own sandbox and approval policy apply.
state = pathlib.Path(os.environ.get("XDG_STATE_HOME", pathlib.Path.home() / ".local/state")) / "tmux-a2a-postman"
socket_directory = pathlib.Path(os.environ["TMUX"].split(",", 1)[0]).parent
command = ["codex", "exec", "--sandbox", "workspace-write", "--add-dir", str(state),
           "--add-dir", str(socket_directory)]
# Preserve only the routing inputs explicitly, even when the user's Codex
# shell_environment_policy filters inherited variables.
for key in ("HOME", "TMUX", "TMUX_PANE", "POSTMAN_BIN", "POSTMAN_CONFIG", "XDG_STATE_HOME"):
    if key in os.environ:
        command.extend(["-c", "shell_environment_policy.set." + key + "=" + json.dumps(os.environ[key])])
completed = subprocess.run(command + ["-"], input=prompt, text=True)
sys.exit(completed.returncode)
