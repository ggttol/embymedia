#!/usr/bin/env python3
import argparse
import base64
import hashlib
import hmac
import html
import json
import secrets
import threading
import time
import urllib.parse
from http import HTTPStatus
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path

SESSION_COOKIE = 'embymedia_http_session'
CSRF_COOKIE = 'embymedia_login_csrf'
MAX_FORM_BYTES = 4096
FAIL_WINDOW_SECONDS = 300
MAX_FAILURES = 5


def b64encode(value: bytes) -> str:
    return base64.urlsafe_b64encode(value).decode().rstrip('=')


def b64decode(value: str) -> bytes:
    return base64.urlsafe_b64decode(value + '=' * ((4 - len(value) % 4) % 4))


class LoginState:
    def __init__(self, path: str):
        config = json.loads(Path(path).read_text())
        self.username = str(config['username'])
        self.salt = b64decode(config['passwordSalt'])
        self.password_hash = b64decode(config['passwordHash'])
        self.iterations = int(config['iterations'])
        self.session_secret = b64decode(config['sessionSecret'])
        self.session_days = min(max(int(config.get('sessionDays', 30)), 1), 30)
        self.authority = str(config.get('authority', 'gaotao.cc:3080'))
        self.failures: dict[str, list[float]] = {}
        self.lock = threading.Lock()

    def verify_password(self, username: str, password: str) -> bool:
        if not hmac.compare_digest(username, self.username):
            return False
        actual = hashlib.pbkdf2_hmac('sha256', password.encode(), self.salt, self.iterations)
        return hmac.compare_digest(actual, self.password_hash)

    def session_value(self) -> tuple[str, int]:
        now = int(time.time())
        expires = now + self.session_days * 86400
        body = b64encode(json.dumps({'v': 1, 'u': self.username, 'iat': now, 'exp': expires}, separators=(',', ':')).encode())
        signature = b64encode(hmac.new(self.session_secret, body.encode(), hashlib.sha256).digest())
        return f'{body}.{signature}', expires

    def valid_session(self, value: str | None) -> bool:
        if value is None:
            return False
        try:
            body, signature = value.split('.', 1)
            expected = b64encode(hmac.new(self.session_secret, body.encode(), hashlib.sha256).digest())
            if not hmac.compare_digest(signature, expected):
                return False
            payload = json.loads(b64decode(body))
            now = int(time.time())
            issued = int(payload.get('iat', 0))
            expires = int(payload.get('exp', 0))
            return (
                payload.get('v') == 1
                and payload.get('u') == self.username
                and issued <= now + 60
                and expires >= now
                and expires - issued <= self.session_days * 86400
            )
        except Exception:
            return False

    def client_key(self, handler: BaseHTTPRequestHandler) -> str:
        # Trust only the Caddy-set X-Real-IP (the real peer); never the raw
        # client-controlled X-Forwarded-For, which would let an attacker rotate
        # buckets and bypass the failure throttle.
        real_ip = handler.headers.get('X-Real-IP', '').strip()
        return real_ip or handler.client_address[0]

    def begin_attempt(self, key: str) -> bool:
        now = time.monotonic()
        cutoff = now - FAIL_WINDOW_SECONDS
        with self.lock:
            current = [value for value in self.failures.get(key, []) if value >= cutoff]
            if len(current) >= MAX_FAILURES:
                self.failures[key] = current
                return False
            current.append(now)
            self.failures[key] = current
            return True

    def clear(self, key: str) -> None:
        with self.lock:
            self.failures.pop(key, None)


class LoginHandler(BaseHTTPRequestHandler):
    server_version = 'EmbyMediaLogin/1'

    @property
    def state(self) -> LoginState:
        return self.server.state  # type: ignore[attr-defined]

    def log_message(self, _format: str, *_args: object) -> None:
        return

    def cookie(self, name: str) -> str | None:
        for segment in self.headers.get('Cookie', '').split(';'):
            key, separator, value = segment.strip().partition('=')
            if separator and key == name:
                return value
        return None

    def safe_redirect(self, value: str | None) -> str:
        if value and value.startswith('/') and not value.startswith('//'):
            return value
        return '/'

    def send_headers(self, status: int, content_type: str = 'text/html; charset=utf-8') -> None:
        self.send_response(status)
        self.send_header('Content-Type', content_type)
        self.send_header('Cache-Control', 'no-store')
        self.send_header('Referrer-Policy', 'no-referrer')
        self.send_header('X-Content-Type-Options', 'nosniff')
        self.send_header('Content-Security-Policy', "default-src 'none'; style-src 'unsafe-inline'; form-action 'self'; base-uri 'none'; frame-ancestors 'none'")

    def render_login(self, rd: str, message: str = '', status: int = HTTPStatus.OK) -> None:
        csrf = secrets.token_urlsafe(24)
        page = f'''<!doctype html><html lang="zh-CN"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>EmbyMedia 登录</title><style>
:root{{color-scheme:dark}}*{{box-sizing:border-box}}body{{margin:0;min-height:100vh;display:grid;place-items:center;background:radial-gradient(circle at 50% 20%,#243d2b 0,#111713 42%,#090c0a 100%);font-family:-apple-system,BlinkMacSystemFont,"Segoe UI",sans-serif;color:#eef5ef}}.card{{width:min(420px,calc(100vw - 32px));padding:34px;border:1px solid #35513d;border-radius:20px;background:rgba(19,28,22,.94);box-shadow:0 24px 80px #0009}}.mark{{font-size:13px;letter-spacing:.14em;color:#8eb89a;text-transform:uppercase}}h1{{margin:10px 0 8px;font-size:28px}}p{{margin:0 0 24px;color:#9eada2}}label{{display:block;margin:14px 0 7px;font-size:13px;color:#c7d3ca}}input{{width:100%;height:46px;border:1px solid #3b4d40;border-radius:11px;background:#0d120f;color:#fff;padding:0 13px;font-size:15px;outline:none}}input:focus{{border-color:#78b88a;box-shadow:0 0 0 3px #78b88a22}}button{{width:100%;height:48px;margin-top:22px;border:0;border-radius:12px;background:#73a982;color:#07130a;font-weight:700;font-size:15px;cursor:pointer}}button:hover{{background:#84ba92}}.error{{padding:11px 12px;border-radius:10px;background:#572328;color:#ffc9cd;margin-bottom:16px;font-size:13px}}.foot{{margin-top:18px;text-align:center;color:#6f8174;font-size:12px}}</style></head><body><main class="card"><div class="mark">EmbyMedia Operations</div><h1>管理登录</h1><p>登录后进入 DSH 运营工作台</p>{f'<div class="error">{html.escape(message)}</div>' if message else ''}<form method="post" action="/login"><input type="hidden" name="csrf" value="{csrf}"><input type="hidden" name="rd" value="{html.escape(rd, quote=True)}"><label for="username">用户名</label><input id="username" name="username" autocomplete="username" required autofocus><label for="password">密码</label><input id="password" name="password" type="password" autocomplete="current-password" required><button type="submit">登录</button></form><div class="foot">HTTP 自用模式 · 请仅通过受控 NPS 入口访问</div></main></body></html>'''
        body = page.encode()
        self.send_headers(status)
        self.send_header('Set-Cookie', f'{CSRF_COOKIE}={csrf}; Max-Age=600; Path=/; SameSite=Strict')
        self.send_header('Content-Length', str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def redirect_login(self) -> None:
        rd = self.headers.get('X-Forwarded-Uri', '/')
        target = '/login?' + urllib.parse.urlencode({'rd': self.safe_redirect(rd)})
        self.send_response(HTTPStatus.FOUND)
        self.send_header('Location', target)
        self.send_header('Cache-Control', 'no-store')
        self.end_headers()

    def do_GET(self) -> None:
        parsed = urllib.parse.urlsplit(self.path)
        if parsed.path == '/health':
            body = b'{"ok":true}'
            self.send_headers(HTTPStatus.OK, 'application/json')
            self.send_header('Content-Length', str(len(body)))
            self.end_headers()
            self.wfile.write(body)
            return
        if parsed.path == '/auth/check':
            if self.state.valid_session(self.cookie(SESSION_COOKIE)):
                self.send_response(HTTPStatus.OK)
                self.send_header('Remote-User', self.state.username)
                self.end_headers()
            else:
                self.redirect_login()
            return
        if parsed.path == '/login':
            rd = self.safe_redirect(urllib.parse.parse_qs(parsed.query).get('rd', ['/'])[0])
            if self.state.valid_session(self.cookie(SESSION_COOKIE)):
                self.send_response(HTTPStatus.SEE_OTHER)
                self.send_header('Location', rd)
                self.end_headers()
                return
            self.render_login(rd)
            return
        if parsed.path == '/logout':
            self.send_response(HTTPStatus.SEE_OTHER)
            self.send_header('Set-Cookie', f'{SESSION_COOKIE}=; Max-Age=0; Path=/; HttpOnly; SameSite=Strict')
            self.send_header('Location', '/login')
            self.end_headers()
            return
        self.send_error(HTTPStatus.NOT_FOUND)

    def do_POST(self) -> None:
        if urllib.parse.urlsplit(self.path).path != '/login':
            self.send_error(HTTPStatus.NOT_FOUND)
            return
        try:
            length = int(self.headers.get('Content-Length', '0'))
        except ValueError:
            length = 0
        if length <= 0 or length > MAX_FORM_BYTES:
            self.send_error(HTTPStatus.BAD_REQUEST)
            return
        form = urllib.parse.parse_qs(self.rfile.read(length).decode(), keep_blank_values=True)
        rd = self.safe_redirect(form.get('rd', ['/'])[0])
        csrf = form.get('csrf', [''])[0]
        csrf_cookie = self.cookie(CSRF_COOKIE) or ''
        if not csrf or not hmac.compare_digest(csrf, csrf_cookie):
            self.render_login(rd, '页面已过期，请重新登录。', HTTPStatus.FORBIDDEN)
            return
        key = self.state.client_key(self)
        if not self.state.begin_attempt(key):
            self.render_login(rd, '尝试次数过多，请 5 分钟后再试。', HTTPStatus.TOO_MANY_REQUESTS)
            return
        username = form.get('username', [''])[0]
        password = form.get('password', [''])[0]
        if not self.state.verify_password(username, password):
            self.render_login(rd, '用户名或密码错误。', HTTPStatus.UNAUTHORIZED)
            return
        self.state.clear(key)
        value, expires = self.state.session_value()
        self.send_response(HTTPStatus.SEE_OTHER)
        self.send_header('Set-Cookie', f'{SESSION_COOKIE}={value}; Max-Age={expires-int(time.time())}; Path=/; HttpOnly; SameSite=Strict')
        self.send_header('Location', rd)
        self.send_header('Cache-Control', 'no-store')
        self.end_headers()


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument('--config', required=True)
    parser.add_argument('--host', default='127.0.0.1')
    parser.add_argument('--port', type=int, default=9092)
    args = parser.parse_args()
    server = ThreadingHTTPServer((args.host, args.port), LoginHandler)
    server.state = LoginState(args.config)  # type: ignore[attr-defined]
    server.serve_forever()


if __name__ == '__main__':
    main()
