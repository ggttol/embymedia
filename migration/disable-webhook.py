#!/usr/bin/env python3
import argparse
import hashlib
import json
import re
import tempfile
from pathlib import Path

parser = argparse.ArgumentParser()
parser.add_argument('--toml', required=True)
parser.add_argument('--inventory', required=True)
args = parser.parse_args()
path = Path(args.toml)
text = path.read_text()
match = re.search(r'([?&]key=)([^&"\']+)', text)
if match is None:
    raise SystemExit('webhook TOML contains no query key')
inventory = json.loads(Path(args.inventory).read_text())
expected = inventory.get('webhook', {}).get('queryKeySha256')
actual = hashlib.sha256(match.group(2).encode()).hexdigest()
if not expected or actual != expected:
    raise SystemExit('webhook sender secret differs from source inventory')
if re.search(r'(?m)^\s*enabled\s*=', text):
    text = re.sub(r'(?m)^(\s*enabled\s*=\s*).+$', r'\1false', text)
else:
    raise SystemExit('webhook TOML has no enabled field')
with tempfile.NamedTemporaryFile('w', dir=path.parent, delete=False) as target:
    target.write(text)
    temporary = Path(target.name)
temporary.chmod(path.stat().st_mode & 0o777)
temporary.replace(path)
print(json.dumps({'path': str(path), 'enabled': False, 'keySha256': actual}))
