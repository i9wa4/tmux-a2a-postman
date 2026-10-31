"""Deterministic Responses API fixture. Codex itself executes every tool call."""
import http.server
import json
import re
import shlex
import threading
import uuid


class ModelFixture:
    def __init__(self, handler, trace):
        self.handler = handler
        self.trace = trace
        fixture = self

        class HTTP(http.server.BaseHTTPRequestHandler):
            def log_message(self, *args):
                pass

            def do_POST(self):
                data = json.loads(self.rfile.read(int(self.headers["Content-Length"])))
                if self.path.endswith("/responses/input_tokens"):
                    payload = json.dumps({"input_tokens": 0}).encode()
                    self.send_response(200)
                    self.send_header("Content-Type", "application/json")
                    self.send_header("Content-Length", str(len(payload)))
                    self.end_headers()
                    self.wfile.write(payload)
                    return
                text = json.dumps(data)
                match = re.search(r"SMOKE_ROLE=(\w+) NONCE=([a-f0-9]+)", text)
                if not match:
                    self.send_error(400, "missing smoke conversation context")
                    return
                role, nonce = match.groups()
                notices = re.findall(r"Postman notification for committed mail (m-[a-f0-9]{32})", text)
                completed = {x.get("call_id") for x in data.get("input", []) if x.get("type") == "function_call_output"}
                pending = [m for m in dict.fromkeys(notices) if "smoke-" + role + "-" + m not in completed]
                message = pending[0] if pending else (notices[-1] if notices else "setup")
                call_id = "smoke-" + role + "-" + message
                outputs = [x for x in data.get("input", []) if x.get("type") == "function_call_output" and x.get("call_id") == call_id]
                fixture.trace.append({"role": role, "phase": message, "context_nonce": nonce, "completed_tool": bool(outputs)})
                if outputs:
                    item = {"type": "message", "role": "assistant", "id": "msg-" + uuid.uuid4().hex,
                            "content": [{"type": "output_text", "text": "SMOKE_IDLE " + role + " " + nonce}]}
                else:
                    command = "python3 " + shlex.quote(fixture.handler) + " " + shlex.quote(message) + " " + shlex.quote(nonce)
                    item = {"type": "function_call", "call_id": call_id, "name": "exec_command",
                            "arguments": json.dumps({"cmd": command, "yield_time_ms": 10000, "max_output_tokens": 4000, "login": False})}
                    # Codex's namespaced function tools require the namespace on the call.
                    if any(t.get("type") == "namespace" and t.get("name") == "functions" for t in data.get("tools", [])):
                        item["namespace"] = "functions"
                rid = "resp-" + uuid.uuid4().hex
                events = [{"type": "response.created", "response": {"id": rid}},
                          {"type": "response.output_item.done", "item": item},
                          {"type": "response.completed", "response": {"id": rid, "usage": {"input_tokens": 0, "output_tokens": 0, "total_tokens": 0}}}]
                payload = "".join("data: " + json.dumps(x) + "\n\n" for x in events).encode()
                self.send_response(200)
                self.send_header("Content-Type", "text/event-stream")
                self.send_header("Content-Length", str(len(payload)))
                self.end_headers()
                self.wfile.write(payload)

        self.server = http.server.ThreadingHTTPServer(("127.0.0.1", 0), HTTP)
        self.thread = threading.Thread(target=self.server.serve_forever, daemon=True)
        self.thread.start()
        self.url = "http://127.0.0.1:" + str(self.server.server_port) + "/v1"

    def close(self):
        self.server.shutdown()
        self.server.server_close()
        self.thread.join()
