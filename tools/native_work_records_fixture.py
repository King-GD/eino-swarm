"""Deterministic keyless local provider for the native work-record case."""
import json
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import threading


PHASES = {
    "create": "Record the measured finding.",
    "retract": "Revise the prior observation using contrary evidence.",
    "restore": "Restore the prior observation using independent confirmation.",
}
TITLE = "Measured native finding"
REFERENCE = "Stable reference"
SUMMARIES = {
    "create": "The measured observation is supported by the initial input.",
    "retract": "The follow-up measurement refutes the observation.",
    "restore": "Independent confirmation supports the observation again.",
}
STATUSES = {"create": "reported", "retract": "retracted", "restore": "reported"}


def call_id(phase, index=0):
    return "native-work-" + phase + "-" + str(index + 1)


class WorkFixture:
    def __init__(self):
        self.requests = []
        self.receipts = {}
        self.errors = []
        self.lock = threading.Lock()
        fixture = self

        class Handler(BaseHTTPRequestHandler):
            def log_message(self, *_):
                pass

            def deny(self, error):
                fixture.errors.append(str(error))
                body = json.dumps({"error": {"message": str(error), "type": "invalid_request_error"}}).encode()
                self.send_response(400)
                self.send_header("Content-Type", "application/json")
                self.send_header("Content-Length", str(len(body)))
                self.end_headers()
                self.wfile.write(body)

            def do_GET(self):
                if self.path != "/v1/models" or self.headers.get("Authorization"):
                    return self.deny(ValueError("unexpected local fixture GET"))
                body = b'{"data":[{"id":"native-work","object":"model"}]}'
                self.send_response(200)
                self.send_header("Content-Type", "application/json")
                self.send_header("Content-Length", str(len(body)))
                self.end_headers()
                self.wfile.write(body)

            def do_POST(self):
                try:
                    if self.path != "/v1/chat/completions" or self.headers.get("Authorization"):
                        raise ValueError("fixture accepts only keyless local chat completions")
                    size = int(self.headers["Content-Length"])
                    if not 0 < size <= 2 * 1024 * 1024:
                        raise ValueError("fixture request size is invalid")
                    body = json.loads(self.rfile.read(size))
                    with fixture.lock:
                        delta, finish = fixture.reply(body)
                except (KeyError, TypeError, ValueError) as error:
                    return self.deny(error)
                if body.get("stream"):
                    def chunk(value, reason):
                        frame = {"id": "native-work", "object": "chat.completion.chunk",
                                 "choices": [{"index": 0, "delta": value, "finish_reason": reason}]}
                        return ("data: " + json.dumps(frame) + "\n\n").encode()
                    raw = chunk(delta, None) + chunk({}, finish) + b"data: [DONE]\n\n"
                    content_type = "text/event-stream"
                else:
                    raw = json.dumps({"id": "native-work", "object": "chat.completion",
                                      "choices": [{"index": 0, "message": delta, "finish_reason": finish}]}).encode()
                    content_type = "application/json"
                self.send_response(200)
                self.send_header("Content-Type", content_type)
                self.send_header("Content-Length", str(len(raw)))
                self.end_headers()
                self.wfile.write(raw)

        self.server = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
        self.server.daemon_threads = True
        self.thread = threading.Thread(target=self.server.serve_forever, args=(.05,), daemon=True)
        self.thread.start()
        self.url = "http://127.0.0.1:" + str(self.server.server_port) + "/v1"

    def __enter__(self):
        return self

    def __exit__(self, *_):
        self.server.shutdown()
        self.server.server_close()
        self.thread.join(timeout=1)

    def reply(self, body):
        if body.get("model") != "native-work":
            raise ValueError("unexpected local model")
        messages = body["messages"]
        if messages and (messages[0].get("content") or "").startswith("## Schedule answer claims"):
            return {"role": "assistant", "content": '{"claims":[]}'}, "stop"
        prompt = next((message.get("content") for message in reversed(messages) if message["role"] == "user"), None)
        phase = next((name for name, text in PHASES.items() if text == prompt), None)
        names = {tool["function"]["name"] for tool in body.get("tools", [])}
        if phase is None or not {"read_work", "record_work", "read"} <= names or "list_models" in names:
            raise ValueError("the request did not use the expected ordinary manager surface")
        commands = self.commands(phase)
        for index, arguments in enumerate(commands):
            tool_id = call_id(phase, index)
            response = next((message for message in reversed(messages)
                             if message["role"] == "tool" and message.get("tool_call_id") == tool_id), None)
            if response is None:
                self.requests.append({"phase": phase, "call": tool_id, "arguments": arguments})
                return {"role": "assistant", "tool_calls": [{"index": 0, "id": tool_id, "type": "function",
                        "function": {"name": "record_work", "arguments": json.dumps(arguments)}}]}, "tool_calls"
            result = json.loads(response["content"])
            node = result.get("node", {})
            if (result.get("ok") is not True or not node.get("id") or node.get("title") != (arguments.get("title") or TITLE)
                    or node.get("status") != STATUSES[phase]
                    or node.get("scope") != ("thread" if phase == "create" else "project")):
                raise ValueError("record_work did not return the expected successful receipt")
            self.receipts[tool_id] = node
        return {"role": "assistant", "content": "The work record update is complete."}, "stop"

    def commands(self, phase):
        if phase == "create":
            return [dict(kind="fact", title=TITLE, summary=SUMMARIES[phase]),
                    dict(kind="fact", title=REFERENCE, summary="The reference remains valid throughout the case.")]
        prior = self.receipts[call_id("create" if phase == "retract" else "retract")]
        version = prior["version"] + (1 if phase == "retract" else 0)
        return [dict(id=prior["id"], version=version, status=STATUSES[phase], summary=SUMMARIES[phase])]
