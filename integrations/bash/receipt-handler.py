#!/usr/bin/env python3
"""Deterministic receipt recipient; acknowledges full reads, never task success."""
import hashlib
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
markdown = archive.read_bytes()  # Complete file through EOF before replying.
message = result["message"]
print(json.dumps({"claimed": message["id"], "archive": str(archive),
                  "full_archive_sha256": hashlib.sha256(markdown).hexdigest()}))
if message["needs_reply"]:
    recipient = message["from"]
    if message.get("from_session"):
        recipient = message["from_session"] + ":" + recipient
    reply = "Full archive read; SHA256=" + hashlib.sha256(markdown).hexdigest() + "\n"
    completed = subprocess.run(
        [os.environ["POSTMAN_BIN"], "send-heredoc", "--postman-md",
         os.environ["POSTMAN_CONFIG"], "--to", recipient, "--reply-to", message["id"],
         "--fills-input-request-id", message["input_request_id"], "--no-reply"],
        input=reply, text=True, check=True,
    )
