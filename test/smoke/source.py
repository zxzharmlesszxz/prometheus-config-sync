#!/usr/bin/env python3
import json
import threading
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path


BASE_CONFIG = Path("/fixtures/prometheus/config").read_bytes()
BASE_RULES = Path("/fixtures/prometheus/rules").read_bytes()
CHANGED_CONFIG = b"""scrape_configs:
  - job_name: generated-config-sync-v2
    static_configs:
      - targets: [\"config-sync:9534\"]
        labels:
          source: generated
          fixture_version: v2
"""
CHANGED_RULES = b"""groups:
  - name: generated-config-sync-example-v2
    rules:
      - record: prometheus_config_sync:fixture_v2
        expr: max(up{job=\"prometheus-config-sync\"})
"""
INVALID_CONFIG = b"scrape_configs: [\n"


class State:
    def __init__(self):
        self.lock = threading.Lock()
        self.mode = "base"
        self.requests = 0

    def snapshot(self):
        with self.lock:
            snapshot_mode = self.mode
            snapshot_requests = self.requests
        return {
            "mode": snapshot_mode,
            "requests": snapshot_requests,
        }

    def get_mode(self):
        with self.lock:
            self.requests += 1
            mode = self.mode
        return mode

    def set_mode(self, mode):
        with self.lock:
            self.mode = mode


STATE = State()


class Handler(BaseHTTPRequestHandler):
    server_version = "prometheus-config-sync-source-smoke/1"

    @staticmethod
    def log_message(message, *args):
        print(json.dumps({"component": "source-smoke", "message": message % args}), flush=True)

    def _reply(self, status, body=b"", content_type="text/plain; charset=utf-8"):
        self.send_response(status)
        self.send_header("Content-Type", content_type)
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def do_GET(self):  # pylint: disable=invalid-name  # noqa: N802
        if self.path == "/healthz":
            self._reply(200, b"ok\n")
            return
        if self.path == "/control/state":
            body = json.dumps(STATE.snapshot()).encode()
            self._reply(200, body, "application/json")
            return
        if self.path == "/metrics":
            requests = STATE.snapshot()["requests"]
            body = (
                "# TYPE prometheus_config_source_fixture_requests_total counter\n"
                f"prometheus_config_source_fixture_requests_total {requests}\n"
            ).encode()
            self._reply(200, body, "text/plain; version=0.0.4")
            return
        if self.path not in ("/prometheus/config", "/prometheus/rules"):
            self._reply(404, b"not found\n")
            return

        mode = STATE.get_mode()
        if mode == "error":
            self._reply(503, b"intentional smoke-test outage\n")
            return

        if self.path == "/prometheus/config":
            if mode == "invalid":
                body = INVALID_CONFIG
            else:
                body = CHANGED_CONFIG if mode == "changed" else BASE_CONFIG
        else:
            body = CHANGED_RULES if mode == "changed" else BASE_RULES
        self._reply(200, body, "application/yaml")
        return

    def do_POST(self):  # pylint: disable=invalid-name  # noqa: N802
        if self.path != "/control/mode":
            self._reply(404, b"not found\n")
            return
        try:
            length = int(self.headers.get("Content-Length", "0"))
            payload = json.loads(self.rfile.read(length) or b"{}")
            mode = payload["mode"]
            if mode not in ("base", "changed", "error", "invalid"):
                raise ValueError("unsupported mode")
        except (KeyError, ValueError, json.JSONDecodeError) as err:
            self._reply(400, f"{err}\n".encode())
            return
        STATE.set_mode(mode)
        self._reply(204)
        return


if __name__ == "__main__":
    server = ThreadingHTTPServer(("0.0.0.0", 9876), Handler)
    server.daemon_threads = True
    server.serve_forever()
