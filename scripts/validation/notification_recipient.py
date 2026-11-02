#!/usr/bin/env python3
"""Recipient action used by terminal fixtures and Codex tools, never the controller."""
import hashlib
import json
import os
import pathlib
import subprocess
import sys
import time

from mail_fixture import read_archive, sent_evidence

os.environ["PATH"] = os.environ["POSTMAN_SMOKE_BIN_DIR"] + ":" + os.environ["PATH"]
phase, nonce = sys.argv[1:]
role = os.environ["POSTMAN_SMOKE_ROLE"]
root = pathlib.Path(os.environ["POSTMAN_SMOKE_EVIDENCE"])
postman, config = os.environ["POSTMAN_BIN"], os.environ["POSTMAN_CONFIG"]
thread_id = os.environ.get("CODEX_THREAD_ID", os.environ["POSTMAN_SMOKE_CONVERSATION"])
case = os.environ["POSTMAN_SMOKE_CASE"]


def failed(kind, error, traceback):
    (root / (role + ".error.txt")).write_text(str(error))
    sys.__excepthook__(kind, error, traceback)


sys.excepthook = failed


def cli(args, body=None):
    result = subprocess.run([postman, *args], input=body, text=True, capture_output=True, check=False)
    if result.returncode:
        (root / (role + ".error.txt")).write_text(result.stderr)
        raise RuntimeError(result.stderr)
    return json.loads(result.stdout)


def receive():
    pop = cli(["pop", "--config", config])
    if not pop["success"]:
        raise RuntimeError("POP failed: " + json.dumps(pop))
    if pop["markdown_absolute_path"] is None:
        # Another genuine hint may already have caused the known batch to be
        # consumed. An empty successful POP is not permission to start polling.
        print("NO_UNREAD_MAIL", role)
        return 0
    archive = pathlib.Path(pop["markdown_absolute_path"])
    full = archive.read_bytes()
    msg = read_archive(archive)
    proof = {"role": role, "conversation_id": thread_id, "context_nonce": nonce, "message": msg,
             "full_archive_sha256": hashlib.sha256(full).hexdigest(), "archive": str(archive)}
    count = len(list(root.glob(role + ".claim.*.json"))) + 1
    body = "Smoke response role=" + role + " nonce=" + nonce + " full-read=" + proof["full_archive_sha256"] + "\n"
    target = None
    if msg["needs_reply"]:
        target = msg["id"]
        proof["send_kind"] = "reply"
    elif role == os.environ["POSTMAN_SMOKE_INITIATOR"] and count == 1 and case != "concurrent":
        target = msg["from_session"] + ":" + msg["from"] if msg.get("from_session") else msg["from"]
        proof["send_kind"] = "request"
    if target:
        if case in ["external", "external-blocked", "external-decision"]:
            state = "blocked" if case == "external-blocked" else "done"
            options = []
            if case == "external-decision" and not msg["external"].get("decision_id"):
                state = "pending"
                options = ["--scope", hashlib.sha256(b"exact deterministic harmless action and context").hexdigest()]
                body += "May I record completion of this exact harmless fixture action?\n"
            proof["send"] = cli(["channel-report-heredoc", "--config", config, "--to", target, "--state", state, *options], body)
        elif case in ["approval", "rejected", "approval-burst"]:
            action = "decline-execution" if case == "rejected" else "approve-execution"
            proof["send"] = cli([action, "--config", config, "--to", target, "--reason", "deterministic smoke decision"])
            state = pathlib.Path(os.environ["XDG_STATE_HOME"]) / "tmux-a2a-postman"
            decisions = [read_archive(path) for path in state.glob("*/agents/" + role + "/sent/*.md")]
            decisions = [item for item in decisions if any(ref["message_id"] == target for ref in item.get("replies", []))]
            if len(decisions) != 1:
                raise RuntimeError("exact decision evidence unavailable")
            proof["send"]["mail_evidence"] = decisions[0]
            proof["decision"] = json.loads(decisions[0]["body"])
        else:
            proof["send"] = cli(["send-heredoc", "--config", config, "--to", target], body)
            proof["send"]["mail_evidence"] = sent_evidence(role, body)
        if proof["send"].get("notification"):
            failure = {"reason": "reply_notification_failed", "notification": proof["send"]["notification"],
                       "handling_message_id": msg["id"], "reply_message_id": proof["send"].get("mail_evidence", {}).get("id")}
            (root / (role + ".error.txt")).write_text(json.dumps(failure))
            raise RuntimeError("reply notification failed: " + json.dumps(failure))
    (root / (role + ".claim." + msg["id"] + ".json")).write_text(json.dumps(proof))
    print("CLAIMED_AND_READ", role, msg["id"], proof["full_archive_sha256"])
    state = proof.get("send", {}).get("self", pop["self"])
    return state.get("unread_count") or 0


if phase == "setup":
    (root / (role + ".ready.json")).write_text(json.dumps({"conversation_id": thread_id, "nonce": nonce}))
    if case == "busy" and role == "worker":
        (root / "busy").touch()
        deadline = time.monotonic() + 30
        while not (root / "release").exists():
            if time.monotonic() > deadline:
                raise RuntimeError("controller did not release busy fixture")
            time.sleep(0.05)
    print("RECIPIENT_READY", role, thread_id)
else:
    remaining = 1  # One genuine notification authorizes the initial POP.
    for _ in range(32):
        if remaining <= 0:
            break
        remaining = receive()  # Continue only from a known positive count.
    if remaining > 0:
        raise RuntimeError("fixture known-mail batch exceeded its finite bound")
