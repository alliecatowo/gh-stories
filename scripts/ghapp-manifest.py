#!/usr/bin/env python3
"""One-click GitHub App creation via the Manifest Flow (local operator tool).

GitHub offers no API to create classic OAuth Apps, but a GitHub App created
from a manifest speaks the exact same user-OAuth protocol our service uses
(empty scope, GET /user, GET /user/following, standard token exchange), so
its client_id/client_secret drop straight into Secret Manager.

Usage:
    python3 scripts/ghapp-manifest.py [--port 8765] [--name gh-stories]

Then open http://127.0.0.1:8765/ in a browser logged into GitHub, press
"Create GitHub App", confirm on GitHub (rename if the slug is taken), and
this script exchanges the callback code for credentials and stores them as
ghs-production-github-client-id / -secret in Secret Manager.

Nothing secret is printed: only the client ID suffix and next steps.
"""
import argparse
import http.server
import json
import secrets
import subprocess
import sys
import threading
import urllib.parse
import urllib.request

GCP_PROJECT = "gh-stories-prod"


def build_manifest(name):
    # Minimal permissions: our user flow only reads the public profile and
    # the public follow graph, which need no extra grant. metadata:read is
    # the mandatory default. No webhook: active:false with a placeholder URL
    # (GitHub requires the url field when hook_attributes is present, so we
    # omit hook_attributes entirely).
    return {
        "name": name,
        "url": "https://github.com/alliecatowo/gh-stories",
        "description": "Stories for GitHub: expiring photo/video stories.",
        "public": False,
        "redirect_url": "",  # filled in per-run with the local callback
        "callback_urls": [],
        "setup_url": "https://github.com/alliecatowo/gh-stories",
        "setup_on_update": False,
        "default_permissions": {"metadata": "read"},
        "default_events": [],
    }


INDEX = """<!doctype html><html><body style="font-family:sans-serif;max-width:40em;margin:4em auto">
<h1>GitHub Stories: create the GitHub App</h1>
<p>Press the button, confirm on GitHub (rename the app there if the suggested
name is taken), and this page's server captures the credentials.</p>
<form action="https://github.com/settings/apps/new?state={state}" method="post">
<input type="hidden" name="manifest" value='{manifest}'>
<button type="submit" style="font-size:1.2em;padding:.5em 1em">Create GitHub App on GitHub</button>
</form></body></html>"""

DONE = """<!doctype html><html><body style="font-family:sans-serif;max-width:40em;margin:4em auto">
<h1>Done.</h1><p>Credentials captured and stored in Secret Manager.
You can close this tab and return to the terminal.</p></body></html>"""


def store_secret(name, value):
    p = subprocess.run(
        ["gcloud", "secrets", "create", name, "--project=" + GCP_PROJECT,
         "--replication-policy=automatic", "--data-file=-"],
        input=value.encode(), capture_output=True)
    if p.returncode != 0 and b"already exists" not in p.stderr:
        raise RuntimeError(p.stderr.decode()[:300])
    if p.returncode != 0:
        p = subprocess.run(
            ["gcloud", "secrets", "versions", "add", name,
             "--project=" + GCP_PROJECT, "--data-file=-"],
            input=value.encode(), capture_output=True)
        p.check_returncode()


def grant(secret):
    for sa in ("gh-stories-api", "gh-stories-worker"):
        subprocess.run(
            ["gcloud", "secrets", "add-iam-policy-binding", secret,
             "--project=" + GCP_PROJECT,
             "--member", f"serviceAccount:{sa}@{GCP_PROJECT}.iam.gserviceaccount.com",
             "--role", "roles/secretmanager.secretAccessor"],
            capture_output=True)


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--port", type=int, default=8765)
    ap.add_argument("--name", default="gh-stories")
    args = ap.parse_args()

    state = secrets.token_urlsafe(24)
    manifest = build_manifest(args.name)
    manifest["redirect_url"] = f"http://127.0.0.1:{args.port}/callback"
    result = {}

    class H(http.server.BaseHTTPRequestHandler):
        def log_message(self, *a):
            pass

        def do_GET(self):
            if self.path == "/":
                body = INDEX.format(
                    state=state,
                    manifest=json.dumps(manifest).replace("'", "&#x27;"))
                self.send_response(200)
                self.send_header("Content-Type", "text/html")
                self.send_header("Content-Length", str(len(body)))
                self.end_headers()
                self.wfile.write(body.encode())
            elif self.path.startswith("/callback"):
                q = urllib.parse.parse_qs(urllib.parse.urlparse(self.path).query)
                if q.get("state", [""])[0] != state or not q.get("code"):
                    self.send_response(400)
                    self.end_headers()
                    self.wfile.write(b"bad state or missing code")
                    return
                code = q["code"][0]
                req = urllib.request.Request(
                    f"https://api.github.com/app-manifests/{code}/conversions",
                    method="POST",
                    headers={"Accept": "application/vnd.github+json",
                             "User-Agent": "gh-stories-manifest/1.0"})
                with urllib.request.urlopen(req) as r:
                    creds = json.load(r)
                result.update(creds)
                body = DONE.encode()
                self.send_response(200)
                self.send_header("Content-Type", "text/html")
                self.send_header("Content-Length", str(len(body)))
                self.end_headers()
                self.wfile.write(body)
                threading.Thread(target=self.server.shutdown,
                                 daemon=True).start()
            else:
                self.send_response(404)
                self.end_headers()

    srv = http.server.HTTPServer(("127.0.0.1", args.port), H)
    print(f"OPEN http://127.0.0.1:{args.port}/  (waiting for the GitHub callback...)",
          flush=True)
    srv.serve_forever()

    for key in ("client_id", "client_secret", "id", "slug", "html_url"):
        if key not in result:
            raise SystemExit(f"conversion response missing {key}: "
                             + json.dumps(result)[:300])
    store_secret("ghs-production-github-client-id", result["client_id"])
    store_secret("ghs-production-github-client-secret", result["client_secret"])
    grant("ghs-production-github-client-id")
    grant("ghs-production-github-client-secret")
    print(f"STORED app {result.get('slug')} (id {result.get('id')}) "
          f"client_id=...{result['client_id'][-4:]} "
          f"{result.get('html_url')}", flush=True)


if __name__ == "__main__":
    sys.exit(main())
