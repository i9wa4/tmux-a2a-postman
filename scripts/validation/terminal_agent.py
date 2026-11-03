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
import time

from mail_fixture import MESSAGE_ID_PATTERN

nonce, mode = sys.argv[1:3]
helper = (sys.argv[3] if len(sys.argv) == 4 else
          str(pathlib.Path(__file__).with_name("notification_recipient.py")))
evidence = pathlib.Path(os.environ["POSTMAN_SMOKE_EVIDENCE"])
role = os.environ["POSTMAN_SMOKE_ROLE"]
submitted = queue.Queue()
lock = threading.RLock()
history, draft = [], bytearray()
absorbed_enters = 0
draft_enters = 0
last_enter_at = 0
submitted_buffers, completed_buffers = 0, 0


def save_progress():
    # A full-read proof can precede helper exit, or another genuine hint's
    # queued empty POP. Publish actual terminal-work completion atomically.
    with lock:
        path = evidence / (role + ".terminal-progress.json")
        temporary = path.with_suffix(".tmp")
        temporary.write_text(json.dumps({"nonce": nonce, "submitted": submitted_buffers,
                                         "completed": completed_buffers}))
        temporary.replace(path)


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
    global completed_buffers
    try:
        action("setup")
        save_progress()
        while True:
            line = submitted.get()
            if line is None:
                return
            # Two independent senders may paste complete hints before either
            # Enter. Accept a sequence of whole notifications, never fragments,
            # interleaved text or duplicate IDs. This is a terminal test actor,
            # not a production notification parser or task runtime.
            notices = re.findall(r"<!-- message start -->.*?<!-- end of message -->", line, re.S)
            remaining = re.sub(r"<!-- message start -->.*?<!-- end of message -->", "", line, flags=re.S)
            if not notices or remaining.strip():
                raise RuntimeError("notification split, interleaved or corrupted: " + line)
            identifiers = []
            for notice in notices:
                matches = re.findall(r"You've got mail: (" + MESSAGE_ID_PATTERN + r")\.md", notice)
                if len(matches) != 1:
                    raise RuntimeError("corrupted notification: " + notice)
                identifiers.extend(matches)
            if len(set(identifiers)) != len(identifiers):
                raise RuntimeError("duplicate notification body pasted")
            for identifier in identifiers:
                action(identifier)
            with lock:
                completed_buffers += 1
                save_progress()
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
                    with (evidence / (role + ".keys.jsonl")).open("a") as trace:
                        trace.write(json.dumps({"at": time.monotonic(), "key": "C-m"}) + "\n")
                    now = time.monotonic()
                    rapid = now - last_enter_at < 0.5
                    last_enter_at = now
                    case = os.environ["POSTMAN_SMOKE_CASE"]
                    absorb_first = case in ["newline-retry", "concurrent", "approval-burst"] and draft and draft_enters == 0
                    absorb_rapid = case == "concurrent" and draft and rapid
                    if role in ["worker", "approver"] and (case == "absorbed-enter" or absorb_first or absorb_rapid):
                        absorbed_enters += 1
                        draft_enters += 1
                        draft.append(10)
                        (evidence / (role + ".absorbed.json")).write_text(json.dumps({"enter_count": absorbed_enters, "draft": draft.decode()}))
                        continue
                    draft_enters = 0
                    line = draft.decode()
                    draft.clear()
                    if line.strip():
                        history.append("agent> " + line)
                        # Cover both substantial real-TUI redraws and minimal
                        # line editors. Only the actual submitted byte buffer
                        # queues recipient work; display changes cannot do so.
                        if case != "minimal-redraw":
                            history.extend(["[terminal submission]", "[recipient work queued]",
                                            "[stdin ready]", "[waiting for next input]"])
                        with (evidence / (role + ".input.jsonl")).open("a") as trace:
                            trace.write(json.dumps({"text": line, "conversation": os.environ["POSTMAN_SMOKE_CONVERSATION"]}) + "\n")
                        submitted_buffers += 1
                        save_progress()
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
