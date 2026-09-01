#!/usr/bin/env python3
import argparse
import json
import re
import urllib.request
from pathlib import Path

parser = argparse.ArgumentParser()
parser.add_argument('--toml', required=True)
parser.add_argument('--url', default='http://127.0.0.1:3080/hooks/clouddrive2')
args = parser.parse_args()
text = Path(args.toml).read_text()
match = re.search(r'[?&]key=([^&"\']+)', text)
if match is None:
    raise SystemExit('webhook TOML contains no query key')
request = urllib.request.Request(
    args.url,
    data=json.dumps({'data': []}).encode(),
    headers={'Content-Type': 'application/json', 'X-Webhook-Secret': match.group(1)},
    method='POST',
)
with urllib.request.urlopen(request, timeout=5) as response:
    if response.status != 200:
        raise SystemExit(f'webhook probe returned {response.status}')
print(json.dumps({'ok': True, 'status': 200}))
