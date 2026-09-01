#!/usr/bin/env python3
import argparse
import base64
import hashlib
import json
import os
import pathlib
import secrets


def encode(value: bytes) -> str:
    return base64.urlsafe_b64encode(value).decode().rstrip('=')




def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument('--password-file', required=True)
    parser.add_argument('--output', default='/etc/embymedia/http-login.json')
    parser.add_argument('--username', default='gaotao')
    args = parser.parse_args()
    password_path = pathlib.Path(args.password_file)
    password = password_path.read_text().rstrip('\n')
    if not password:
        raise SystemExit('login password must not be empty')
    output = pathlib.Path(args.output)
    iterations = 600_000
    salt = secrets.token_bytes(24)
    derived = hashlib.pbkdf2_hmac('sha256', password.encode(), salt, iterations)
    session_secret = secrets.token_bytes(32)
    config = {
        'username': args.username,
        'passwordSalt': encode(salt),
        'passwordHash': encode(derived),
        'iterations': iterations,
        'sessionSecret': encode(session_secret),
        'sessionDays': 30,
        'authority': 'gaotao.cc:3080',
    }
    temporary = output.with_suffix('.json.tmp')
    temporary.write_text(json.dumps(config, indent=2) + '\n')
    os.chmod(temporary, 0o640)
    os.replace(temporary, output)
    password_path.unlink(missing_ok=True)
    print(json.dumps({'output': str(output), 'username': args.username, 'sessionDays': 30}))


if __name__ == '__main__':
    main()
