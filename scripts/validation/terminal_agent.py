"""Deterministic agent console: only submitted terminal input triggers mail work."""
import json
import os
import pathlib
import re
import subprocess
import sys
import termios
import tty

nonce, mode = sys.argv[1:]
helper = str(pathlib.Path(__file__).with_name("notification_recipient.py"))
evidence = pathlib.Path(os.environ["POSTMAN_SMOKE_EVIDENCE"])
role = os.environ["POSTMAN_SMOKE_ROLE"]


def action(phase):
    result = subprocess.run([sys.executable, helper, phase, nonce], text=True, capture_output=True)
    print(result.stdout.replace("\n", "\r\n"), end="", flush=True)
    if result.returncode:
        raise RuntimeError(result.stderr)


saved = termios.tcgetattr(0)
try:
    if mode == "bracketed":
        tty.setraw(0)
        print("\x1b[?2004h", end="", flush=True)
    action("setup")
    print("agent> ", end="", flush=True)
    pending, pasted = bytearray(), False
    while True:
        if mode == "plain":
            line = sys.stdin.readline()
            if not line:
                break
        else:
            byte = os.read(0, 1)
            if not byte:
                break
            if byte == b"\x1b":
                sequence = byte
                while len(sequence) < 6:
                    piece = os.read(0, 6 - len(sequence))
                    if not piece:
                        raise RuntimeError("truncated terminal sequence")
                    sequence += piece
                if sequence == b"\x1b[200~":
                    pasted = True
                elif sequence == b"\x1b[201~":
                    pasted = False
                else:
                    raise RuntimeError("unexpected terminal sequence " + repr(sequence))
                continue
            if byte not in [b"\r", b"\n"] or pasted:
                pending += byte
                continue
            line = pending.decode()
            pending.clear()
            print(line + "\r\n", end="", flush=True)
        if not line.strip():
            continue
        with (evidence / (role + ".input.jsonl")).open("a") as trace:
            trace.write(json.dumps({"text": line, "conversation": os.environ["POSTMAN_SMOKE_CONVERSATION"]}) + "\n")
        matches = re.findall(r"You've got mail: (m-[a-f0-9]{32})\.md", line)
        if len(matches) != 1 or "<!-- message start -->" not in line or "<!-- end of message -->" not in line:
            raise RuntimeError("notification was split, interleaved or corrupted: " + line)
        action(matches[0])
        print("agent> ", end="", flush=True)
except Exception as error:
    (evidence / (role + ".error.txt")).write_text(str(error))
    raise
finally:
    if mode == "bracketed":
        print("\x1b[?2004l", end="", flush=True)
    termios.tcsetattr(0, termios.TCSANOW, saved)
