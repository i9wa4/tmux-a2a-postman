#!/usr/bin/env python3
"""Agent-independent tmux notification tests; deterministic terminal agents by default."""
import argparse
import concurrent.futures
import hashlib
import json
import os
import pathlib
import shlex
import shutil
import subprocess
import tempfile
import time
import uuid

from codex_smoke_model import ModelFixture

CASES = ["rally", "busy", "concurrent", "parent-child", "child-parent", "approval", "rejected", "disabled"]
parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument("--agent", choices=["fixture", "codex"], default="fixture")
parser.add_argument("--terminal", choices=["both", "plain", "bracketed"], default="both")
parser.add_argument("--case", choices=["all", *CASES], default="all")
parser.add_argument("--mode", choices=["mock", "live"], default="mock")
parser.add_argument("--postman", required=True)
parser.add_argument("--postman-md", type=pathlib.Path,
                    help="copy an existing orchestrator/worker/approver topology unchanged (rally/approval/rejected)")
parser.add_argument("--codex", default="codex")
parser.add_argument("--model", default="gpt-6-luna")
parser.add_argument("--baseline", action="store_true", help="expect unmodified main to fail to notify (rally only)")
parser.add_argument("--timeout", type=int, default=30, help="seconds per awaited step")
parser.add_argument("--output", type=pathlib.Path)
args = parser.parse_args()
if args.mode == "live" and args.agent != "codex":
    parser.error("--mode live requires --agent codex; terminal fixtures are deterministic")
if args.postman_md and args.case not in ["rally", "approval", "rejected"]:
    parser.error("--postman-md requires --case rally, approval or rejected")
postman = str(pathlib.Path(args.postman).resolve())
codex = shutil.which(args.codex) if args.agent == "codex" else None
if not shutil.which("tmux") or (args.agent == "codex" and not codex):
    parser.error("tmux is required; --agent codex additionally needs Codex")
if args.baseline and args.case != "rally":
    parser.error("--baseline requires --case rally")
recipient = str(pathlib.Path(__file__).with_name("notification_recipient.py").resolve())


def run_case(case, terminal):
    # Short canonical paths avoid macOS Unix-socket limits and /tmp aliases
    # disagreeing with the agent's trusted-project cwd.
    root = pathlib.Path(tempfile.mkdtemp(prefix="pm-input-", dir="/tmp")).resolve()
    home, evidence = root / "home", root / "evidence"
    work, ch = home / "work", home / ".codex"
    work.mkdir(parents=True)
    if codex:
        ch.mkdir(mode=0o700)
    evidence.mkdir()
    bin_dir = root / "bin"
    bin_dir.mkdir()
    for name, path in {"tmux-a2a-postman": postman, "codex": codex, "tmux": shutil.which("tmux")}.items():
        if path:
            (bin_dir / name).symlink_to(path)
    cross, approval = case in ["parent-child", "child-parent"], case in ["approval", "rejected"]
    roles = ["orchestrator", "approver" if approval else "worker"]
    initiator = "worker" if case == "child-parent" else "orchestrator"
    receiver = next(role for role in roles if role != initiator)
    dirs = {role: work for role in roles}
    sessions = {role: "work" for role in roles}
    if cross:
        dirs["worker"] = home / "child"
        dirs["worker"].mkdir()
        sessions["worker"] = "child"
    configs = {role: dirs[role] / "postman.md" for role in roles}
    for role in roles:
        config = "## 1. `edges`\n```mermaid\ngraph LR\norchestrator --- " + roles[1] + "\n"
        config += "class approver command_approver_node\n" if approval else ""
        config += "```\n"
        config += ("\n## 2. `common_template`\nShared instructions are not a node.\n"
                   "\n## 3. `message_footer`\nPresentation text is not a node.\n")
        if cross:
            config = ("---\ninterface_node: " + role + "\nworkspace_tree:\n"
                      "  - session_name: work\n    interface_node: orchestrator\n"
                      "  - session_name: child\n    parent_session_name: work\n    interface_node: worker\n---\n" + config)
        if args.postman_md:
            configs[role].write_bytes(args.postman_md.read_bytes())
        else:
            configs[role].write_text(config)
    trace = []
    model = ModelFixture(recipient, trace) if codex and args.mode == "mock" else None
    socket = root / "tmux.sock"
    env = dict(os.environ, HOME=str(home), CODEX_HOME=str(ch), XDG_STATE_HOME=str(root / "state"),
               PATH=str(bin_dir) + ":" + os.environ["PATH"], TERM="tmux-256color",
               POSTMAN_SMOKE_BIN_DIR=str(bin_dir), POSTMAN_BIN=postman, POSTMAN_SMOKE_EVIDENCE=str(evidence),
               POSTMAN_SMOKE_BASELINE="1" if args.baseline else "0", POSTMAN_SMOKE_CASE=case,
               POSTMAN_SMOKE_INITIATOR=initiator)
    env.pop("CODEX_THREAD_ID", None)
    if args.mode == "live":
        auth_home = pathlib.Path(os.environ.get("CODEX_HOME", pathlib.Path.home() / ".codex"))
        if (auth_home / "auth.json").is_file():
            (ch / "auth.json").symlink_to(auth_home / "auth.json")
    settings = ('model = ' + json.dumps(args.model) + '\nmodel_reasoning_effort = "medium"\n[tui]\nterminal_title = []\n'
                '[shell_environment_policy]\ninherit = "all"\n')
    for directory in set(dirs.values()):
        settings += "[projects." + json.dumps(str(directory)) + ']\ntrust_level = "trusted"\n'
    if model:
        settings = ('model_provider = "postman_smoke"\n' + settings + '\n[model_providers.postman_smoke]\n'
                    'name = "Postman smoke fixture"\nwire_api = "responses"\nrequires_openai_auth = false\nbase_url = ' + json.dumps(model.url) + '\n')
    if codex:
        (ch / "config.toml").write_text(settings)
    tm = ["tmux", "-u", "-S", str(socket)]
    started, panes = False, {}
    nonces = {role: uuid.uuid4().hex for role in roles}
    report = {"case": case, "agent": args.agent, "terminal": terminal, "mode": args.mode, "baseline": args.baseline, "root": str(root)}
    if args.postman_md:
        report["topology_source"] = str(args.postman_md.resolve())
        report["topology_sha256"] = hashlib.sha256(configs[initiator].read_bytes()).hexdigest()
    if codex:
        report["codex_version"] = subprocess.check_output([codex, "--version"], env=env, text=True).strip()

    def tmux(*commands):
        return subprocess.check_output(tm + list(commands), env=env, text=True).strip()

    def wait_for(description, check):
        end = time.monotonic() + args.timeout
        next_probe = 0
        while time.monotonic() < end:
            errors = list(evidence.glob("*.error.txt"))
            if errors:
                raise RuntimeError(errors[0].read_text())
            found = check()
            if found:
                return found
            if time.monotonic() >= next_probe:
                for role, pane in panes.items():
                    if tmux("display-message", "-p", "-t", pane, "#{pane_dead}") == "1":
                        raise RuntimeError(role + " exited before " + description)
                next_probe = time.monotonic() + 0.5
            time.sleep(0.1)
        raise RuntimeError("timed out: " + description)

    def sender_env(role):
        return dict(env, TMUX=str(socket) + ",0,0", TMUX_PANE=panes[role])

    def send():
        to = sessions[receiver] + ":" + receiver if cross else receiver
        began = time.monotonic()
        initial = subprocess.run([postman, "send-heredoc", "--postman-md", str(configs[initiator]), "--to", to, "--reply-required"],
                                 input="Smoke ping; full archive read required.\nUnicode 日本語\nEOF_MARKER\n", env=sender_env(initiator),
                                 text=True, capture_output=True, timeout=15)
        if initial.returncode:
            raise RuntimeError("send-heredoc failed after " + str(time.monotonic() - began) + "s: " +
                               initial.stdout + initial.stderr)
        return {"seconds": time.monotonic() - began, **json.loads(initial.stdout)}

    def verify_proof(proof, ready):
        role = proof["role"]
        if proof["conversation_id"] != ready[role]["conversation_id"] or proof["context_nonce"] != nonces[role]:
            raise RuntimeError("recipient conversation changed")
        raw = pathlib.Path(proof["archive"]).read_bytes()
        if hashlib.sha256(raw).hexdigest() != proof["full_archive_sha256"]:
            raise RuntimeError("full archive digest mismatch")
        if "send" in proof:
            msg, reply = proof["message"], proof["send"]["message"]
            if reply["reply_to"] != msg["id"] or reply["fills_input_request_id"] != msg["input_request_id"]:
                raise RuntimeError("reply correlation mismatch")

    try:
        ready = {}
        # Sequential initial startup avoids racing Codex's first SQLite migration.
        for role in roles:
            command = "new-session" if not panes or cross else "split-window"
            options = ["-s", sessions[role]] if command == "new-session" else ["-t", panes[roles[0]]]
            pane = tmux("-f", "/dev/null", command, "-d", *options, "-c", str(dirs[role]), "-P", "-F", "#{pane_id}", "exec sleep 600")
            started = True
            panes[role] = pane
            tmux("set-option", "-t", sessions[role], "remain-on-exit", "on")
            tmux("resize-window", "-t", pane, "-x", "240", "-y", "60")
            tmux("select-pane", "-t", pane, "-T", role)
            prompt = ("Isolated Postman notification smoke. SMOKE_ROLE=" + role + " NONCE=" + nonces[role] + ". "
                      "Do not edit project files or use external services. Run python3 " + shlex.quote(recipient) + " setup " + nonces[role] +
                      ". Then wait for Postman notifications in THIS conversation. For each notification, run python3 " + shlex.quote(recipient) +
                      " <exact-notified-message-id> " + nonces[role] + ". The helper POPs, reads the complete archive and sends the correlated test reply. Do not poll.")
            launch = ["env", "POSTMAN_SMOKE_ROLE=" + role, "POSTMAN_CONFIG=" + str(configs[role]),
                      "POSTMAN_SMOKE_CONVERSATION=" + uuid.uuid4().hex]
            if codex:
                # Real vde-layout-style Codex; transport is still ordinary tmux input.
                launch += [codex, "--no-daemon", "--no-alt-screen", "--model", args.model,
                           "--dangerously-bypass-approvals-and-sandbox", prompt]
            else:
                launch += [shutil.which("python3"), "-u", str(pathlib.Path(__file__).with_name("terminal_agent.py")), nonces[role], terminal]
            tmux("respawn-pane", "-k", "-t", pane, "exec " + shlex.join(launch))
            path = evidence / (role + ".ready.json")
            wait_for(role + " setup", lambda p=path: p.exists() and p.stat().st_size)
            ready[role] = json.loads(path.read_text())
            print(json.dumps({"event": "recipient_ready", "case": case, "role": role, "conversation_id": ready[role]["conversation_id"]}), flush=True)
        report["conversations"] = ready
        if case == "disabled":
            tmux("copy-mode", "-t", panes[receiver])
        if case == "busy":
            wait_for("recipient busy in a real Codex tool", lambda: (evidence / "busy").exists())
        if approval:
            marker = work / "executed"
            command = "printf approved > " + shlex.quote(str(marker))
            execution = subprocess.Popen([postman, "execute-bash", "--postman-md", str(configs[initiator]), "--command", command],
                                         env=sender_env(initiator), text=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
            try:
                stdout, stderr = execution.communicate(timeout=args.timeout)
            except subprocess.TimeoutExpired:
                execution.kill()
                execution.communicate()
                raise RuntimeError("approval did not complete")
            result = json.JSONDecoder().raw_decode(stderr)[0]["execution"]
            wait_for("reviewer evidence", lambda: len(list(evidence.glob("approver.claim.*.json"))) == 1)
            proof = json.loads(next(evidence.glob("approver.claim.*.json")).read_text())
            verify_proof(proof, ready)
            expected = "rejected" if case == "rejected" else "executed"
            if result["status"] != expected or result["notice"]["status"] != "accepted" or result["reply_id"] != proof["send"]["message"]["id"] or result["request_id"] != proof["message"]["id"]:
                raise RuntimeError("approval did not use the exact notification-driven decision: " + stderr)
            if marker.exists() != (case == "approval") or (execution.returncode == 0) != (case == "approval"):
                raise RuntimeError("approval execution policy mismatch")
            report.update(execution=result, claims=[proof], command_stdout=stdout)
        else:
            if case == "concurrent":
                with concurrent.futures.ThreadPoolExecutor(max_workers=2) as pool:
                    sends = list(pool.map(lambda _: send(), range(2)))
            else:
                sends = [send()]
            report["sends"] = sends
            if args.baseline or case == "disabled":
                if sends[0]["notice"]["status"] != "unavailable":
                    raise RuntimeError("negative control unexpectedly accepted notification")
                time.sleep(0.5)  # No event-driven receiver action may occur without terminal input.
                if list(evidence.glob("*.claim.*.json")):
                    raise RuntimeError("recipient acted without a working notification route")
                inboxes = list((root / "state").glob("**/inbox/" + sends[0]["message"]["id"] + ".md"))
                if len(inboxes) != 1:
                    raise RuntimeError("notification failure lost durable delivery")
                report["negative_control"] = "durable unread mail; no recipient action"
            else:
                if any(s["notice"]["status"] != "accepted" or s["seconds"] > 5 for s in sends):
                    raise RuntimeError("notification failed or send waited for the recipient: " + json.dumps(sends))
                if case == "busy":
                    time.sleep(1)
                    if list(evidence.glob("*.claim.*.json")):
                        raise RuntimeError("busy recipient unexpectedly handled another notification")
                    (evidence / "release").touch()
                    report["queued_while_busy"] = True
                wait_for("notification-driven rally", lambda: len(list(evidence.glob("*.claim.*.json"))) == 4)
                proofs = [json.loads(path.read_text()) for path in evidence.glob("*.claim.*.json")]
                by_id = {proof["message"]["id"]: proof for proof in proofs}
                visited = set()
                for sent in sends:
                    current = sent["message"]
                    for _ in range(2 if case == "concurrent" else 4):
                        proof = by_id[current["id"]]
                        verify_proof(proof, ready)
                        visited.add(current["id"])
                        if "send" in proof:
                            current = proof["send"]["message"]
                if len(visited) != 4:
                    raise RuntimeError("duplicate or missing notification claim")
                report["claims"] = proofs
        if case == "rally":
            # All four claims have finished here. Approval requesters can still
            # be processing an already-claimed decision hint after the wrapper
            # returns; sampling their short-lived lock is not an idle boundary.
            status = subprocess.run([postman, "get-status", "--postman-md", str(configs[initiator])],
                                    env=sender_env(initiator), text=True, capture_output=True, timeout=15)
            if status.returncode:
                raise RuntimeError("final get-status failed: " + status.stdout + status.stderr)
            report["final_status"] = json.loads(status.stdout)
            if not report["final_status"]["self"]["known"]:
                raise RuntimeError("final mailbox status unavailable")
        report["result"] = "pass"
    except Exception as error:
        report["result"], report["error"] = "failed", str(error)
    finally:
        if started:
            try:
                report["screens"] = {role: tmux("capture-pane", "-p", "-S", "-", "-t", pane) for role, pane in panes.items()}
            finally:
                subprocess.run(tm + ["kill-server"], env=env, check=False, capture_output=True)
        if model:
            model.close()
        report["model_fixture_trace"] = trace
        if codex and args.mode == "mock" and report["result"] != "pass":
            report["runtime_logs"] = {p.name: p.read_text(errors="replace")[-4000:]
                                      for p in (ch / "log").glob("*.log")}
        (root / "report.json").write_text(json.dumps(report, indent=2))
        print(json.dumps({"case": case, "result": report["result"], "report": str(root / "report.json")}), flush=True)
        if report["result"] != "pass":
            print(json.dumps({"error": report.get("error"), "screens": {r: s[-4000:] for r, s in report.get("screens", {}).items()},
                              "runtime_logs": report.get("runtime_logs", {})}), flush=True)
    return report


reports = []
terminals = ["plain", "bracketed"] if args.terminal == "both" and not codex else ["bracketed" if codex else args.terminal]
for terminal in terminals:
    for selected in CASES if args.case == "all" else [args.case]:
        reports.append(run_case(selected, terminal))
        if reports[-1]["result"] != "pass":
            break
    if reports[-1]["result"] != "pass":
        break
output = args.output or pathlib.Path(reports[0]["root"]) / "suite.json"
output.parent.mkdir(parents=True, exist_ok=True)
output.write_text(json.dumps({"result": "pass" if all(r["result"] == "pass" for r in reports) else "failed", "cases": reports}, indent=2))
print(json.dumps({"suite_report": str(output)}), flush=True)
raise SystemExit(0 if all(r["result"] == "pass" for r in reports) else 1)
