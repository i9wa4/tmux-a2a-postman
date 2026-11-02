"""Deterministic terminal editor: only submitted input triggers queued mail work."""
import json
import os
import pathlib
import queue
import re
import subprocess
import sys
import termios
import threading
import tty

nonce, mode = sys.argv[1:3]
helper = (sys.argv[3] if len(sys.argv) == 4 else
          str(pathlib.Path(__file__).with_name("notification_recipient.py")))
evidence = pathlib.Path(os.environ["POSTMAN_SMOKE_EVIDENCE"])
role = os.environ["POSTMAN_SMOKE_ROLE"]
submitted = queue.Queue()
lock = threading.RLock()
history, draft = [], bytearray()
absorbed_enters = 0


def render():
    with lock:
        rows = "\r\n".join(history[-35:])
        sys.stdout.write("\x1b[H\x1b[2J" + rows + ("\r\n" if rows else "") + "agent> " + draft.decode(errors="replace"))
        sys.stdout.flush()


def action(phase):
    result = subprocess.run([sys.executable, helper, phase, nonce], text=True, capture_output=True)
    with lock:
        history.extend(result.stdout.splitlines())
        render()
    if result.returncode:
        raise RuntimeError(result.stderr)


def process():
    try:
        action("setup")
        while True:
            line = submitted.get()
            if line is None:
                return
            matches = re.findall(r"You've got mail: (m-[a-f0-9]{32})\.md", line)
            if len(matches) != 1 or "<!-- message start -->" not in line or "<!-- end of message -->" not in line:
                raise RuntimeError("notification split, interleaved or corrupted: " + line)
            action(matches[0])
    except Exception as error:
        (evidence / (role + ".error.txt")).write_text(str(error))
        os._exit(1)


saved = termios.tcgetattr(0)
try:
    # Both modes have an editor; plain advertises no bracketed-paste support.
    tty.setraw(0)
    if mode == "bracketed":
        print("\x1b[?2004h", end="", flush=True)
    render()
    threading.Thread(target=process, daemon=True).start()
    pending, pasted = bytearray(), False
    while True:
        data = os.read(0, 4096)
        if not data:
            break
        pending += data
        with lock:
            while pending:
                if pending[0] == 27:
                    if len(pending) < 6:
                        break
                    sequence = bytes(pending[:6])
                    del pending[:6]
                    if sequence == b"\x1b[200~":
                        pasted = True
                    elif sequence == b"\x1b[201~":
                        pasted = False
                    else:
                        raise RuntimeError("unexpected terminal sequence " + repr(sequence))
                    continue
                value = pending.pop(0)
                if value in [10, 13] and not pasted:
                    if os.environ["POSTMAN_SMOKE_CASE"] == "absorbed-enter" and role == "worker":
                        absorbed_enters += 1
                        draft.append(10)
                        (evidence / (role + ".absorbed.json")).write_text(json.dumps({"enter_count": absorbed_enters, "draft": draft.decode()}))
                        continue
                    line = draft.decode()
                    draft.clear()
                    if line.strip():
                        history.append("agent> " + line)
                        with (evidence / (role + ".input.jsonl")).open("a") as trace:
                            trace.write(json.dumps({"text": line, "conversation": os.environ["POSTMAN_SMOKE_CONVERSATION"]}) + "\n")
                        submitted.put(line)
                else:
                    draft.append(value)
            render()
except Exception as error:
    (evidence / (role + ".error.txt")).write_text(str(error))
    raise
finally:
    submitted.put(None)
    if mode == "bracketed":
        print("\x1b[?2004l", end="", flush=True)
    termios.tcsetattr(0, termios.TCSANOW, saved)
