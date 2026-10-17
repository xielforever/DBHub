#!/usr/bin/env python3
"""Fetch only locked HTTPS artifacts and verify the committed SHA-256 pins."""
import hashlib
import json
import os
from pathlib import Path
import ssl
import sys
import urllib.request

lock = Path(sys.argv[1])
destination = Path(sys.argv[2])
destination.mkdir(parents=True, exist_ok=True)
context = ssl.create_default_context(cafile=os.environ.get("SSL_CERT_FILE"))
for artifact in json.loads(lock.read_text()):
    name = artifact["file"]
    if Path(name).name != name or not artifact["url"].startswith("https://repo.maven.apache.org/maven2/"):
        raise SystemExit("invalid locked artifact")
    path = destination / name
    data = path.read_bytes() if path.exists() else urllib.request.urlopen(artifact["url"], context=context, timeout=60).read()
    if hashlib.sha256(data).hexdigest() != artifact["sha256"]:
        raise SystemExit("artifact checksum mismatch: " + name)
    if not path.exists():
        temporary = path.with_suffix(".download")
        temporary.write_bytes(data)
        temporary.replace(path)
    print("Verified " + artifact["id"])
