#!/usr/bin/env python3
"""Exercise a real tmux #() status job through a private terminal client."""

import argparse
import fcntl
import os
import pathlib
import pty
import selectors
import shlex
import struct
import subprocess
import termios
import time

parser = argparse.ArgumentParser(description=__doc__)
for name in ["postman", "socket", "config", "home", "state", "wrong-socket", "output"]:
    parser.add_argument("--" + name, required=True)
args = parser.parse_args()


def tmux(*values):
    return subprocess.run(["tmux", "-S", args.socket, *values], check=True, capture_output=True, timeout=5)


for name, value in {"HOME": args.home, "XDG_STATE_HOME": args.state,
                    "TMUX": args.wrong_socket + ",0,0", "TMUX_PANE": "%999999"}.items():
    tmux("set-environment", "-g", name, value)
tmux("set-option", "-g", "status", "2")
tmux("set-option", "-g", "status-interval", "1")
tmux("set-option", "-g", "status-format[0]", "palette 🟢🟡🟠🔴⚫ 🟩🟨🟧🟥⬛ 💚💛🧡❤️🖤")
command = ("env -u TMUX_PANE TMUX='#{socket_path},#{pid},0' " + shlex.quote(args.postman) +
           " get-status-oneline --config " + shlex.quote(args.config) + " 2>/dev/null")
tmux("set-option", "-g", "status-format[1]", "#[align=right]#(" + command + ")")

master, slave = pty.openpty()
fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack("HHHH", 24, 160, 0, 0))


def terminal_session():
    os.setsid()
    fcntl.ioctl(0, termios.TIOCSCTTY, 0)


env = {key: value for key, value in os.environ.items() if key not in {"TMUX", "TMUX_PANE"}}
env["TERM"] = "xterm-256color"
process = subprocess.Popen(["tmux", "-S", args.socket, "attach-session", "-t", "work"],
                           stdin=slave, stdout=slave, stderr=slave, env=env, preexec_fn=terminal_session)
os.close(slave)
selector = selectors.DefaultSelector()
selector.register(master, selectors.EVENT_READ)
raw = bytearray()
expected = "[0]⬛🖤".encode()
heart = "❤️".encode()
try:
    deadline = time.monotonic() + 8
    while expected not in raw or heart not in raw:
        if process.poll() is not None or time.monotonic() > deadline:
            raise RuntimeError("actual tmux status job did not render the selected server and complete red-heart sequence: " + repr(bytes(raw[-5000:])))
        for key, _ in selector.select(0.2):
            raw.extend(os.read(key.fd, 65536))
    pathlib.Path(args.output).write_bytes(raw)
    print("actual tmux #() job: selected socket, no agent pane, full emoji sequences: PASS")
finally:
    selector.close()
    process.terminate()
    try:
        process.wait(timeout=2)
    except subprocess.TimeoutExpired:
        process.kill()
        process.wait(timeout=2)
    os.close(master)
