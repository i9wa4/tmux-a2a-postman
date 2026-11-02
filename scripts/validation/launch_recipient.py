"""Launch-preset fixture actor: only submitted notifications trigger own tools."""
import hashlib
import json
import os
import pathlib
import subprocess
import sys

from mail_fixture import read_archive

phase, nonce = sys.argv[1:]
role = os.environ["POSTMAN_SMOKE_ROLE"]
evidence = pathlib.Path(os.environ["POSTMAN_SMOKE_EVIDENCE"])
postman = os.environ["POSTMAN_BIN"]


def cli(args, body=None):
    # Deliberately no --config: the product launcher must preserve its selected
    # non-default policy in the actual console/tool environment.
    result = subprocess.run([postman, *args], input=body, text=True,
                            capture_output=True, timeout=20, check=False)
    if result.returncode:
        raise RuntimeError(result.stdout + result.stderr)
    return json.loads(result.stdout)


def receive():
    result = cli(["pop"])
    if not result["success"]:
        raise RuntimeError("POP failed")
    path = result["markdown_absolute_path"]
    if path is None:
        return 0
    archive = pathlib.Path(path)
    full = archive.read_bytes()
    message = read_archive(archive)
    proof = {"archive": path, "message": message,
             "sha256": hashlib.sha256(full).hexdigest(), "nonce": nonce,
             "selected_config": os.environ.get("TMUX_A2A_POSTMAN_CONFIG")}
    if "LAUNCH_SELECTED_COMMON_POLICY" not in full.decode():
        raise RuntimeError("wrong selected common policy")
    if message.get("kind") == "execution-request":
        request = json.loads(message["body"])
        expected = "printf executed > " + os.environ["POSTMAN_LAUNCH_QUOTED_MARKER"]
        if role != "reviewer" or request["reason"] != "launch protocol check" or request["command"] != expected:
            raise RuntimeError("execution request conditions changed")
        proof["decision"] = cli(["approve-execution", "--to", message["id"],
                                 "--reason", "fixed launch fixture approval"])
        own = proof["decision"]["self"]
    elif message["needs_reply"]:
        proof["reply"] = cli(["send-heredoc", "--to", message["id"]],
                             "exact launched reply " + nonce)
        own = proof["reply"]["self"]
    else:
        own = result["self"]
    (evidence / (role + ".claim." + message["id"] + ".json")).write_text(json.dumps(proof))
    return own.get("unread_count") or 0


if phase == "setup":
    (evidence / (role + ".ready.json")).write_text(json.dumps({
        "nonce": nonce, "config": os.environ.get("TMUX_A2A_POSTMAN_CONFIG")}))
    print("LAUNCH_RECIPIENT_READY", role)
else:
    remaining = 1
    for _ in range(16):
        if remaining <= 0:
            break
        remaining = receive()
    if remaining:
        raise RuntimeError("finite launched actor batch exhausted")
