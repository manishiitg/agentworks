#!/usr/bin/env python3
"""Register official local reference MCPs for testing; never grant group access."""
import argparse
import json
from pathlib import Path
from urllib.parse import urlparse
from urllib.request import Request, urlopen


def loopback_url(value):
    parsed = urlparse(value)
    if (parsed.scheme != "http" or parsed.hostname not in ("127.0.0.1", "::1", "localhost")
            or parsed.username or parsed.password or parsed.query or parsed.fragment
            or parsed.path not in ("", "/")):
        raise argparse.ArgumentTypeError("use a loopback HTTP origin without credentials or a path")
    return value.rstrip("/")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--gateway", type=loopback_url, default="http://127.0.0.1:8080")
    parser.add_argument("--fixtures", type=loopback_url, default="http://127.0.0.1:18164")
    parser.add_argument("--admin-token-file", required=True)
    args = parser.parse_args()
    token = Path(args.admin_token_file).read_text().strip()
    if not token:
        parser.error("admin token file is empty")

    def request(method, path, body=None):
        encoded = json.dumps(body).encode() if body is not None else None
        req = Request(args.gateway + path, data=encoded, method=method,
                      headers={"Authorization": "Bearer " + token, "Content-Type": "application/json"})
        with urlopen(req, timeout=40) as response:
            return json.load(response)

    existing = request("GET", "/api/admin/connectors")["connectors"]
    for name in ("filesystem", "memory"):
        url = args.fixtures + "/" + name + "/mcp"
        # Store response field names are exported Go names.
        found = next((c for c in existing if c.get("UpstreamURL") == url), None)
        if found:
            request("POST", "/api/admin/connectors/" + found["ID"] + "/sync")
            print("Synced local " + name)
        else:
            request("POST", "/api/admin/connectors", {
                "provider": "local-" + name, "label": "Local test · " + name.capitalize(), "url": url,
            })
            print("Connected local " + name)
    print("New connections approve their initial tool definitions. No group or policy access was granted.")


if __name__ == "__main__":
    main()
