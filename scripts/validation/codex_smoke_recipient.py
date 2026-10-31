#!/usr/bin/env python3
"""Recipient tool used by the smoke model; never called by the controller."""
import hashlib
import json
import os
import pathlib
import subprocess
import sys
import time

os.environ["PATH"] = os.environ["POSTMAN_SMOKE_BIN_DIR"] + ":" + os.environ["PATH"]
phase, nonce = sys.argv[1:]
role = os.environ["POSTMAN_SMOKE_ROLE"]
root = pathlib.Path(os.environ["POSTMAN_SMOKE_EVIDENCE"])
postman, config = os.environ["POSTMAN_BIN"], os.environ["POSTMAN_CONFIG"]
thread_id = os.environ["CODEX_THREAD_ID"]
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


if phase == "setup":
    if os.environ.get("POSTMAN_SMOKE_BASELINE") != "1":
        cli(["register-notifier", "--runtime", "codex", "--postman-md", config])
    (root / (role + ".ready.json")).write_text(json.dumps({"thread_id": thread_id, "nonce": nonce}))
    if case == "busy" and role == "worker":
        (root / "busy").touch()
        deadline = time.monotonic() + 30
        while not (root / "release").exists():
            if time.monotonic() > deadline:
                raise RuntimeError("controller did not release busy fixture")
            time.sleep(0.05)
    print("REGISTERED", role, thread_id)
else:
    pop = cli(["pop", "--postman-md", config, phase])
    if pop["status"] != "message":
        raise RuntimeError("notification did not produce a claim: " + json.dumps(pop))
    archive = pathlib.Path(pop["markdown_absolute_path"])
    full = archive.read_bytes()
    msg = pop["message"]
    proof = {"role": role, "thread_id": thread_id, "context_nonce": nonce, "message": msg,
             "full_archive_sha256": hashlib.sha256(full).hexdigest(), "archive": str(archive)}
    count = len(list(root.glob(role + ".claim.*.json"))) + 1
    if msg["needs_reply"]:
        to = msg["from_session"] + ":" + msg["from"] if msg.get("from_session") else msg["from"]
        # Three response legs after the controller's single initial send.
        required = case not in ["concurrent", "approval", "rejected"] and (role == os.environ["POSTMAN_SMOKE_INITIATOR"] or count == 1)
        args = ["send-heredoc", "--postman-md", config, "--to", to,
                "--reply-to", msg["id"], "--fills-input-request-id", msg["input_request_id"],
                "--reply-required" if required else "--no-reply"]
        body = "Smoke response role=" + role + " nonce=" + nonce + " full-read=" + proof["full_archive_sha256"] + "\n"
        if case in ["approval", "rejected"]:
            request = json.loads(full.decode().split("-->\n", 1)[1])
            proof["decision"] = {"version": 1, "command": request["command"], "decision": "rejected" if case == "rejected" else "approved"}
            body = json.dumps(proof["decision"])
        proof["send"] = cli(args, body)
        if proof["send"]["notice"]["status"] != "accepted":
            raise RuntimeError("reply notification failed: " + json.dumps(proof["send"]))
    (root / (role + ".claim." + msg["id"] + ".json")).write_text(json.dumps(proof))
    print("CLAIMED_AND_READ", role, msg["id"], proof["full_archive_sha256"])
