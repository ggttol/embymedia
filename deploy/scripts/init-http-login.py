#!/usr/bin/env python3
import argparse
import base64
import hashlib
import json
import os
import pathlib
import secrets
import time


def encode(value: bytes) -> str:
    return base64.urlsafe_b64encode(value).decode().rstrip('=')


def password_fields(password: str, iterations: int) -> dict[str, object]:
    salt = secrets.token_bytes(24)
    derived = hashlib.pbkdf2_hmac('sha256', password.encode(), salt, iterations)
    return {
        'passwordSalt': encode(salt),
        'passwordHash': encode(derived),
        'iterations': iterations,
    }


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument('--password-file', required=True)
    parser.add_argument('--output', default='/srv/embymedia/data/auth/http-login.json')
    parser.add_argument('--username', default='gaotao')
    args = parser.parse_args()
    password = pathlib.Path(args.password_file).read_text().rstrip('\n')
    if not password:
        raise SystemExit('login password must not be empty')
    if len(password) < 10 or len(password) > 256:
        raise SystemExit('login password must contain 10 to 256 characters')
    output = pathlib.Path(args.output)
    output.parent.mkdir(parents=True, exist_ok=True)
    iterations = 600_000
    timestamp = time.strftime('%Y-%m-%dT%H:%M:%SZ', time.gmtime())
    admin = {
        'username': args.username,
        'role': 'admin',
        'enabled': True,
        'sessionVersion': encode(secrets.token_bytes(18)),
        'createdAt': timestamp,
        'updatedAt': timestamp,
        **password_fields(password, iterations),
    }
    config = {
        'version': 2,
        'sessionSecret': encode(secrets.token_bytes(32)),
        'sessionDays': 30,
        'authority': 'gaotao.cc:3080',
        'legacySessionExpiresAt': 0,
        'users': [admin],
        'username': admin['username'],
        'passwordSalt': admin['passwordSalt'],
        'passwordHash': admin['passwordHash'],
        'iterations': admin['iterations'],
    }
    temporary = output.with_name(output.name + '.tmp')
    temporary.write_text(json.dumps(config, indent=2, ensure_ascii=False) + '\n')
    os.chmod(temporary, 0o600)
    os.replace(temporary, output)
    print(json.dumps({'output': str(output), 'username': args.username, 'sessionDays': 30}))


if __name__ == '__main__':
    main()
