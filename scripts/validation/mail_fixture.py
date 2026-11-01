"""Read-only evidence helpers for isolated mail fixtures, never recipient actions."""
import json
import os
import pathlib


def read_archive(path):
    raw = pathlib.Path(path).read_bytes()
    header, body = raw.split(b" -->\n\n", 1)
    if not header.startswith(b"<!-- postman "):
        raise RuntimeError("invalid fixture mail header")
    message = json.loads(header[len(b"<!-- postman "):])
    if message.get("guidance_version") == 1:
        body, delimiter, footer = body.rpartition(b"\n\n---\n<!-- postman reply guidance v1 -->\n")
        if not delimiter or not footer.endswith(b"<!-- end postman reply guidance v1 -->\n"):
            raise RuntimeError("fixture archive lacks its generated guidance")
    message["body"] = body.decode()
    return message


def sent_evidence(role, body, state_home=None):
    state = pathlib.Path(state_home or os.environ["XDG_STATE_HOME"]) / "tmux-a2a-postman"
    found = [read_archive(path) for path in state.glob("*/agents/" + role + "/sent/*.md")]
    found = [message for message in found if message["body"] == body]
    if len(found) != 1:
        raise RuntimeError("cannot identify unique own sent evidence")
    return found[0]
