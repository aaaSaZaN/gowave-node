#!/usr/bin/env python3
"""Fetch a checksum-verified Xray release for integration tests, never production."""
import hashlib
import json
import os
import pathlib
import platform
import sys
import urllib.request
import zipfile
import io

version, destination = sys.argv[1:]
arch = {'x86_64': '64', 'aarch64': 'arm64-v8a'}[platform.machine()]
endpoint = 'latest' if version == 'latest' else 'tags/' + version
request = urllib.request.Request('https://api.github.com/repos/XTLS/Xray-core/releases/' + endpoint, headers={'User-Agent': 'Remnanode-tests'})
metadata = json.load(urllib.request.urlopen(request, timeout=30))
asset = next(a for a in metadata['assets'] if a['name'] == f'Xray-linux-{arch}.zip')
response = urllib.request.urlopen(asset['browser_download_url'], timeout=120)
if not response.url.startswith('https://'): raise ValueError('Unsafe redirect')
data = response.read(128 * 1024 * 1024 + 1)
if len(data) > 128 * 1024 * 1024: raise ValueError('Oversized archive')
expected = asset.get('digest', '')
if expected != 'sha256:' + hashlib.sha256(data).hexdigest(): raise ValueError('Missing or mismatched release digest')
directory = pathlib.Path(destination)
directory.mkdir(parents=True, exist_ok=True)
with zipfile.ZipFile(io.BytesIO(data)) as archive:
    for name in ('xray', 'geoip.dat', 'geosite.dat'):
        (directory / ('rw-core' if name == 'xray' else name)).write_bytes(archive.read(name))
os.chmod(directory / 'rw-core', 0o755)
print(metadata['tag_name'])
