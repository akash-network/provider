"""Small HTTP fixture for local provider lifecycle and gateway validation."""
import json
import os
from pathlib import Path
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from urllib.request import urlopen

identity = os.environ.get("IDENTITY", "validation")
value_file = Path(os.environ.get("STORAGE_DIR", "/tmp")) / "validation-value"


class Handler(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"

    def reply(self, body, status=200):
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def do_GET(self):
        if self.path == "/backend":
            with urlopen(os.environ["BACKEND_URL"], timeout=5) as response:
                self.reply(response.read())
        elif self.path == "/value":
            self.reply(value_file.read_bytes() if value_file.exists() else b"empty")
        elif self.path == "/stream":
            self.send_response(200)
            self.send_header("Content-Length", "6")
            self.end_headers()
            for chunk in (b"a\n", b"b\n", b"c\n"):
                self.wfile.write(chunk)
                self.wfile.flush()
                time.sleep(0.1)
        else:
            self.reply(json.dumps({"identity": identity, "pod": os.environ.get("HOSTNAME", "")}).encode())

    def do_POST(self):
        body = self.rfile.read(int(self.headers.get("Content-Length", "0")))
        if self.path == "/value":
            value_file.write_bytes(body)
        self.reply(body)


ThreadingHTTPServer(("0.0.0.0", 8080), Handler).serve_forever()
