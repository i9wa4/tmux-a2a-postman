"""Read-only evidence helpers for isolated mail fixtures, never recipient actions."""
import hashlib
import json
import os
import pathlib

# Transport validation remains in Go. Fixtures accept both canonical wire shapes
# while retaining the complete exact notification ID in their evidence.
MESSAGE_ID_PATTERN = r"m-(?:[0-9a-f]{32}|[0-9]{8}T[0-9]{6}\.[0-9]{9}Z-(?:[a-f0-9]{32}|[A-Za-z0-9]{6}))"


def workspace_files(state, pattern):
    """Enumerate real fixture workspaces once; display aliases duplicate none."""
    state = pathlib.Path(state)
    if state.exists():
        for workspace in state.iterdir():
            if not workspace.is_symlink() and workspace.is_dir():
                yield from workspace.glob(pattern)


def read_archive(path):
    raw = pathlib.Path(path).read_bytes()
    header, body = raw.split(b" -->\n\n", 1)
    if not header.startswith(b"<!-- postman "):
        raise RuntimeError("invalid fixture mail header")
    message = json.loads(header[len(b"<!-- postman "):])
    context = message.get("context")
    if context is not None:
        size = context.get("bytes")
        if context.get("version") != 1 or type(size) is not int or not 0 < size <= len(body) or context.get("markdown"):
            raise RuntimeError("invalid fixture recipient-context descriptor")
        prefix, body = body[:size], body[size:]
        if hashlib.sha256(prefix).hexdigest() != context.get("sha256"):
            raise RuntimeError("fixture recipient context failed integrity validation")
        message["recipient_context"] = prefix.decode()
    elif message.get("guidance_version") == 1:
        body, delimiter, footer = body.rpartition(b"\n\n---\n<!-- postman reply guidance v1 -->\n")
        if not delimiter or not footer.endswith(b"<!-- end postman reply guidance v1 -->\n"):
            raise RuntimeError("fixture archive lacks its generated guidance")
    message["body"] = body.decode()
    return message


def sent_evidence(role, body, state_home=None):
    state = pathlib.Path(state_home or os.environ["XDG_STATE_HOME"]) / "tmux-a2a-postman"
    found = [read_archive(path) for path in workspace_files(state, "agents/" + role + "/sent/*.md")]
    found = [message for message in found if message["body"] == body]
    if len(found) != 1:
        raise RuntimeError("cannot identify unique own sent evidence")
    return found[0]
