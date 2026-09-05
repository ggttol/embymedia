#!/usr/bin/env python3
import argparse
import base64
import hashlib
import hmac
import html
import json
import os
import re
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
MAX_JSON_BYTES = 16384
FAIL_WINDOW_SECONDS = 300
MAX_FAILURES = 5
PASSWORD_ITERATIONS = 600_000
USERNAME_PATTERN = re.compile(r'[A-Za-z0-9._-]{3,64}')
ROLES = {'admin', 'operator'}


def b64encode(value: bytes) -> str:
    return base64.urlsafe_b64encode(value).decode().rstrip('=')


def b64decode(value: str) -> bytes:
    return base64.urlsafe_b64decode(value + '=' * ((4 - len(value) % 4) % 4))


def now_text() -> str:
    return time.strftime('%Y-%m-%dT%H:%M:%SZ', time.gmtime())


def validate_username(username: str) -> str:
    normalized = username.strip()
    if not USERNAME_PATTERN.fullmatch(normalized):
        raise ValueError('用户名必须为 3–64 位字母、数字、点、下划线或连字符。')
    return normalized


def validate_password(password: str) -> None:
    if len(password) < 10 or len(password) > 256:
        raise ValueError('密码长度必须为 10–256 个字符。')


def password_fields(password: str, iterations: int = PASSWORD_ITERATIONS) -> dict[str, object]:
    validate_password(password)
    salt = secrets.token_bytes(24)
    derived = hashlib.pbkdf2_hmac('sha256', password.encode(), salt, iterations)
    return {
        'passwordSalt': b64encode(salt),
        'passwordHash': b64encode(derived),
        'iterations': iterations,
    }


def public_user(user: dict[str, object]) -> dict[str, object]:
    return {
        'username': user['username'],
        'role': user['role'],
        'enabled': user['enabled'],
        'created_at': user['createdAt'],
        'updated_at': user['updatedAt'],
    }


class LoginState:
    def __init__(self, path: str):
        self.path = Path(path)
        config = json.loads(self.path.read_text())
        self.session_secret = b64decode(str(config['sessionSecret']))
        self.session_days = min(max(int(config.get('sessionDays', 30)), 1), 30)
        self.authority = str(config.get('authority', 'gaotao.cc:3080'))
        self.legacy_session_expires_at = int(config.get('legacySessionExpiresAt', 0))
        self.failures: dict[str, list[float]] = {}
        self.lock = threading.RLock()
        self.users: dict[str, dict[str, object]] = {}
        configured_users = config.get('users')
        if isinstance(configured_users, list) and configured_users:
            for raw_user in configured_users:
                user = self._normalize_user(raw_user)
                self.users[str(user['username'])] = user
        else:
            self.legacy_session_expires_at = int(time.time()) + 86400
            username = validate_username(str(config['username']))
            timestamp = now_text()
            self.users[username] = self._normalize_user({
                'username': username,
                'role': 'admin',
                'enabled': True,
                'passwordSalt': config['passwordSalt'],
                'passwordHash': config['passwordHash'],
                'iterations': config['iterations'],
                'sessionVersion': b64encode(secrets.token_bytes(18)),
                'createdAt': timestamp,
                'updatedAt': timestamp,
            })
            self._persist_locked()
        if not any(bool(user['enabled']) and user['role'] == 'admin' for user in self.users.values()):
            raise ValueError('at least one enabled administrator is required')

    def _normalize_user(self, raw_user: object) -> dict[str, object]:
        if not isinstance(raw_user, dict):
            raise ValueError('user records must be objects')
        username = validate_username(str(raw_user['username']))
        role = str(raw_user.get('role', 'operator'))
        if role not in ROLES:
            raise ValueError(f'invalid role for {username}')
        timestamp = now_text()
        return {
            'username': username,
            'role': role,
            'enabled': bool(raw_user.get('enabled', True)),
            'passwordSalt': str(raw_user['passwordSalt']),
            'passwordHash': str(raw_user['passwordHash']),
            'iterations': int(raw_user.get('iterations', PASSWORD_ITERATIONS)),
            'sessionVersion': str(raw_user.get('sessionVersion') or b64encode(secrets.token_bytes(18))),
            'createdAt': str(raw_user.get('createdAt') or timestamp),
            'updatedAt': str(raw_user.get('updatedAt') or timestamp),
        }

    def _persist_locked(self) -> None:
        config = {
            'version': 2,
            'sessionSecret': b64encode(self.session_secret),
            'sessionDays': self.session_days,
            'authority': self.authority,
            'legacySessionExpiresAt': self.legacy_session_expires_at,
            'users': [self.users[name] for name in sorted(self.users)],
        }
        compatibility_admin = next(user for user in self.users.values() if bool(user['enabled']) and user['role'] == 'admin')
        config.update({
            'username': compatibility_admin['username'],
            'passwordSalt': compatibility_admin['passwordSalt'],
            'passwordHash': compatibility_admin['passwordHash'],
            'iterations': compatibility_admin['iterations'],
        })
        temporary = self.path.with_name(self.path.name + '.tmp')
        temporary.write_text(json.dumps(config, indent=2, ensure_ascii=False) + '\n')
        os.chmod(temporary, 0o600)
        os.replace(temporary, self.path)

    def verify_password(self, username: str, password: str) -> bool:
        with self.lock:
            user = self.users.get(username)
            if user is None or not bool(user['enabled']):
                return False
            salt = b64decode(str(user['passwordSalt']))
            expected = b64decode(str(user['passwordHash']))
            iterations = int(user['iterations'])
        actual = hashlib.pbkdf2_hmac('sha256', password.encode(), salt, iterations)
        return hmac.compare_digest(actual, expected)

    def session_value(self, username: str) -> tuple[str, int]:
        with self.lock:
            user = self.users[username]
            session_version = str(user['sessionVersion'])
            role = str(user['role'])
        now = int(time.time())
        expires = now + self.session_days * 86400
        payload = {'v': 2, 'u': username, 'r': role, 'sv': session_version, 'iat': now, 'exp': expires}
        body = b64encode(json.dumps(payload, separators=(',', ':')).encode())
        signature = b64encode(hmac.new(self.session_secret, body.encode(), hashlib.sha256).digest())
        return f'{body}.{signature}', expires

    def session_user(self, value: str | None) -> dict[str, object] | None:
        if value is None:
            return None
        try:
            body, signature = value.split('.', 1)
            expected = b64encode(hmac.new(self.session_secret, body.encode(), hashlib.sha256).digest())
            if not hmac.compare_digest(signature, expected):
                return None
            payload = json.loads(b64decode(body))
            now = int(time.time())
            issued = int(payload.get('iat', 0))
            expires = int(payload.get('exp', 0))
            if issued > now + 60 or expires < now or expires - issued > self.session_days * 86400:
                return None
            username = str(payload.get('u', ''))
            with self.lock:
                user = self.users.get(username)
                if user is None or not bool(user['enabled']):
                    return None
                version = int(payload.get('v', 0))
                if version == 1 and now > self.legacy_session_expires_at:
                    return None
                if version == 2 and not hmac.compare_digest(str(payload.get('sv', '')), str(user['sessionVersion'])):
                    return None
                if version not in (1, 2):
                    return None
                return public_user(user)
        except Exception:
            return None

    def list_users(self) -> list[dict[str, object]]:
        with self.lock:
            return [public_user(self.users[name]) for name in sorted(self.users)]

    def create_user(self, username: str, password: str, role: str) -> dict[str, object]:
        username = validate_username(username)
        validate_password(password)
        if role not in ROLES:
            raise ValueError('角色必须是管理员或操作员。')
        with self.lock:
            if username in self.users:
                raise ValueError('该用户名已经存在。')
            timestamp = now_text()
            user: dict[str, object] = {
                'username': username,
                'role': role,
                'enabled': True,
                'sessionVersion': b64encode(secrets.token_bytes(18)),
                'createdAt': timestamp,
                'updatedAt': timestamp,
                **password_fields(password),
            }
            self.users[username] = user
            self._persist_locked()
            return public_user(user)

    def update_user(self, actor: str, username: str, updates: dict[str, object]) -> dict[str, object]:
        with self.lock:
            user = self.users.get(username)
            if user is None:
                raise KeyError(username)
            role = str(updates.get('role', user['role']))
            if role not in ROLES:
                raise ValueError('角色必须是管理员或操作员。')
            enabled_value = updates.get('enabled', user['enabled'])
            if not isinstance(enabled_value, bool):
                raise ValueError('enabled 必须是布尔值。')
            enabled = enabled_value
            if username == actor and (not enabled or role != 'admin'):
                raise ValueError('不能停用当前管理员或移除自己的管理员权限。')
            enabled_admins = sum(
                1 for name, candidate in self.users.items()
                if name != username and bool(candidate['enabled']) and candidate['role'] == 'admin'
            )
            if bool(user['enabled']) and user['role'] == 'admin' and (not enabled or role != 'admin') and enabled_admins == 0:
                raise ValueError('系统必须保留至少一个启用的管理员。')
            password = updates.get('password')
            changed = role != user['role'] or enabled != user['enabled']
            user['role'] = role
            user['enabled'] = enabled
            if password is not None and str(password):
                user.update(password_fields(str(password)))
                changed = True
            if changed:
                user['sessionVersion'] = b64encode(secrets.token_bytes(18))
                user['updatedAt'] = now_text()
                self._persist_locked()
            return public_user(user)

    def delete_user(self, actor: str, username: str) -> None:
        with self.lock:
            user = self.users.get(username)
            if user is None:
                raise KeyError(username)
            if username == actor:
                raise ValueError('不能删除当前登录用户。')
            if bool(user['enabled']) and user['role'] == 'admin':
                enabled_admins = sum(bool(candidate['enabled']) and candidate['role'] == 'admin' for candidate in self.users.values())
                if enabled_admins <= 1:
                    raise ValueError('系统必须保留至少一个启用的管理员。')
            del self.users[username]
            self._persist_locked()

    def client_key(self, handler: BaseHTTPRequestHandler) -> str:
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
    server_version = 'EmbyMediaLogin/2'

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
        self.send_header('X-Frame-Options', 'DENY')
        self.send_header('Content-Security-Policy', "default-src 'none'; style-src 'unsafe-inline'; form-action 'self'; base-uri 'none'; frame-ancestors 'none'")

    def send_json(self, status: int, value: object) -> None:
        body = json.dumps(value, ensure_ascii=False, separators=(',', ':')).encode()
        self.send_headers(status, 'application/json; charset=utf-8')
        self.send_header('Content-Length', str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def render_login(self, rd: str, message: str = '', status: int = HTTPStatus.OK) -> None:
        csrf = secrets.token_urlsafe(24)
        message_markup = f'<p class="error" role="alert">{html.escape(message)}</p>' if message else ''
        page = '''<!doctype html>
<html lang="zh-CN">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<meta name="theme-color" content="#ebe5d6">
<title>登录 · EmbyMedia</title>
<style>
:root{--paper:#ebe5d6;--surface:#f8f5eb;--ink:#1d241f;--muted:#596158;--line:#c9c0ac;--forest:#164b38;--red:#a6432f;--soft:#e2ddcf}*{box-sizing:border-box}html{min-width:320px;background:var(--paper)}body{margin:0;min-height:100vh;color:var(--ink);font-family:"PingFang SC","Hiragino Sans GB","Microsoft YaHei",sans-serif;background:linear-gradient(rgba(29,36,31,.025) 1px,transparent 1px),var(--paper);background-size:100% 32px}.shell{width:min(1120px,calc(100% - 32px));min-height:100vh;margin:auto;display:grid;grid-template-columns:minmax(0,1.15fr) minmax(360px,.85fr);align-items:stretch}.context{padding:clamp(44px,8vw,108px) clamp(28px,7vw,88px) 56px 0;display:flex;flex-direction:column;justify-content:space-between}.brand{display:flex;align-items:center;gap:14px}.mark{display:grid;width:48px;height:58px;place-items:center;background:var(--forest);color:#fff;font:700 15px Georgia,serif;box-shadow:6px 6px 0 rgba(22,75,56,.13)}.brand strong{display:block;font:700 22px Georgia,"Songti SC",serif}.brand small,.eyebrow{font:700 10px "SFMono-Regular",Consolas,monospace;letter-spacing:.17em;color:var(--red)}.statement{max-width:630px;margin:64px 0}.statement h1{margin:0;font:700 clamp(46px,7vw,86px)/.98 Georgia,"Songti SC",serif;letter-spacing:-.055em}.statement h1 span{color:var(--red)}.statement p{max-width:560px;margin:24px 0 0;font-size:16px;line-height:1.9;color:var(--muted)}.ledger{display:grid;grid-template-columns:repeat(3,1fr);border-top:1px solid var(--ink);border-bottom:1px solid var(--line)}.ledger div{min-height:96px;padding:18px 16px;border-right:1px solid var(--line)}.ledger div:last-child{border:0}.ledger strong{display:block;font:700 13px Georgia,"Songti SC",serif}.ledger span{display:block;margin-top:9px;font-size:12px;line-height:1.6;color:var(--muted)}.panel{min-height:100vh;padding:clamp(32px,7vw,88px) clamp(24px,5vw,60px);display:flex;align-items:center;border-left:1px solid var(--line);background:rgba(248,245,235,.78)}.card{width:100%;padding:clamp(26px,4vw,42px);border:1px solid var(--line);background:var(--surface);box-shadow:0 20px 60px rgba(67,56,36,.08)}.card .step{font:700 10px "SFMono-Regular",Consolas,monospace;letter-spacing:.16em;color:var(--red)}h2{margin:12px 0 8px;font:700 31px Georgia,"Songti SC",serif}.intro{margin:0 0 28px;color:var(--muted);font-size:14px;line-height:1.7}.error{margin:0 0 20px;padding:12px 14px;border-left:3px solid var(--red);background:#f2e2dc;color:#7d2f22;font-size:13px}label{display:block;margin:16px 0 8px;font-size:13px;font-weight:600}input{width:100%;min-height:48px;padding:0 14px;border:1px solid var(--line);border-radius:0;background:#fffdf6;color:var(--ink);font:inherit}input:focus{outline:2px solid var(--red);outline-offset:2px;border-color:var(--forest)}button{width:100%;min-height:50px;margin-top:24px;border:1px solid var(--forest);border-radius:0;background:var(--forest);color:#fff;font:700 14px inherit;cursor:pointer;box-shadow:5px 5px 0 rgba(22,75,56,.14)}button:hover{background:#0f382a}.privacy{margin:20px 0 0;padding-top:18px;border-top:1px solid var(--line);font-size:12px;line-height:1.7;color:var(--muted)}@media(max-width:800px){.shell{width:100%;display:block}.context{min-height:auto;padding:28px 20px 24px}.statement{margin:46px 0 34px}.statement h1{font-size:48px}.ledger{grid-template-columns:1fr}.ledger div{min-height:auto;border-right:0;border-bottom:1px solid var(--line)}.panel{min-height:auto;padding:24px 16px 40px;border-left:0;border-top:1px solid var(--line)}.card{padding:26px 22px}}@media(prefers-reduced-motion:no-preference){button{transition:background .18s ease,transform .18s ease}button:active{transform:translate(2px,2px)}}
</style>
<style>@media(max-width:800px){.context{padding:22px 20px 14px}.statement{margin:30px 0 18px}.statement h1{font-size:42px}.statement p{margin-top:14px;font-size:14px;line-height:1.7}.ledger{display:none}.panel{padding-top:20px}}</style>
</head>
<body>
<main class="shell">
<section class="context" aria-labelledby="access-title">
<div class="brand"><span class="mark">EM</span><span><strong>EmbyMedia</strong><small>MEDIA OPERATIONS</small></span></div>
<div class="statement"><p class="eyebrow">PRIVATE OPERATIONS ACCESS</p><h1 id="access-title">进入你的<br><span>媒体控制台。</span></h1><p>集中管理 115、CloudDrive2、Emby、STRM 自动任务与 Agent 接入。仅向明确授权的用户开放。</p></div>
<div class="ledger" aria-label="访问说明"><div><strong>独立运行</strong><span>Go 服务与本地登录代理</span></div><div><strong>凭据隔离</strong><span>密码不会进入业务 API</span></div><div><strong>会话保护</strong><span>最长 30 天并可失效</span></div></div>
</section>
<aside class="panel"><form class="card" method="post" action="/login"><span class="step">ACCESS / 01</span><h2>身份验证</h2><p class="intro">使用管理员为你创建的账户继续。</p>__MESSAGE__<input type="hidden" name="csrf" value="__CSRF__"><input type="hidden" name="rd" value="__RD__"><label for="username">用户名</label><input id="username" name="username" autocomplete="username" required autofocus><label for="password">密码</label><input id="password" name="password" type="password" autocomplete="current-password" required><button type="submit">进入控制台</button><p class="privacy">这是私有运维系统。登录失败会按来源地址限速；请勿在共享设备上保存密码。</p></form></aside>
</main>
</body>
</html>'''
        page = page.replace('__MESSAGE__', message_markup).replace('__CSRF__', html.escape(csrf, quote=True)).replace('__RD__', html.escape(rd, quote=True))
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

    def current_user(self) -> dict[str, object] | None:
        return self.state.session_user(self.cookie(SESSION_COOKIE))

    def require_admin(self) -> dict[str, object] | None:
        user = self.current_user()
        if user is None:
            self.send_json(HTTPStatus.UNAUTHORIZED, {'error': '登录已失效，请重新登录。'})
            return None
        if user['role'] != 'admin':
            self.send_json(HTTPStatus.FORBIDDEN, {'error': '只有管理员可以管理用户。'})
            return None
        return user

    def read_json(self) -> dict[str, object]:
        if not self.headers.get('Content-Type', '').lower().startswith('application/json'):
            raise ValueError('请求必须使用 application/json。')
        origin = self.headers.get('Origin', '')
        if origin and urllib.parse.urlsplit(origin).netloc != self.state.authority:
            raise PermissionError('请求来源不受信任。')
        try:
            length = int(self.headers.get('Content-Length', '0'))
        except ValueError as error:
            raise ValueError('请求长度无效。') from error
        if length <= 0 or length > MAX_JSON_BYTES:
            raise ValueError('请求内容为空或过大。')
        value = json.loads(self.rfile.read(length))
        if not isinstance(value, dict):
            raise ValueError('请求内容必须是 JSON 对象。')
        return value

    def user_path(self) -> str | None:
        prefix = '/auth/users/'
        path = urllib.parse.urlsplit(self.path).path
        if not path.startswith(prefix):
            return None
        username = urllib.parse.unquote(path[len(prefix):])
        return username if username and '/' not in username else None

    def clear_session(self, redirect: bool) -> None:
        status = HTTPStatus.SEE_OTHER if redirect else HTTPStatus.NO_CONTENT
        self.send_response(status)
        self.send_header('Set-Cookie', f'{SESSION_COOKIE}=; Max-Age=0; Path=/; HttpOnly; SameSite=Strict')
        if redirect:
            self.send_header('Location', '/login')
        self.send_header('Cache-Control', 'no-store')
        self.end_headers()

    def do_GET(self) -> None:
        parsed = urllib.parse.urlsplit(self.path)
        if parsed.path == '/health':
            self.send_json(HTTPStatus.OK, {'ok': True})
            return
        if parsed.path == '/auth/check':
            user = self.current_user()
            if user is None:
                self.redirect_login()
            else:
                self.send_response(HTTPStatus.OK)
                self.send_header('Remote-User', str(user['username']))
                self.send_header('Remote-Role', str(user['role']))
                self.end_headers()
            return
        if parsed.path == '/auth/me':
            user = self.current_user()
            if user is None:
                self.send_json(HTTPStatus.UNAUTHORIZED, {'error': '登录已失效，请重新登录。'})
            else:
                self.send_json(HTTPStatus.OK, {'user': user})
            return
        if parsed.path == '/auth/users':
            if self.require_admin() is not None:
                self.send_json(HTTPStatus.OK, {'users': self.state.list_users()})
            return
        if parsed.path == '/login':
            rd = self.safe_redirect(urllib.parse.parse_qs(parsed.query).get('rd', ['/'])[0])
            if self.current_user() is not None:
                self.send_response(HTTPStatus.SEE_OTHER)
                self.send_header('Location', rd)
                self.end_headers()
            else:
                self.render_login(rd)
            return
        if parsed.path == '/logout':
            self.clear_session(True)
            return
        self.send_error(HTTPStatus.NOT_FOUND)

    def do_POST(self) -> None:
        path = urllib.parse.urlsplit(self.path).path
        if path == '/logout':
            self.clear_session(False)
            return
        if path == '/auth/users':
            if self.require_admin() is None:
                return
            try:
                value = self.read_json()
                user = self.state.create_user(str(value.get('username', '')), str(value.get('password', '')), str(value.get('role', 'operator')))
                self.send_json(HTTPStatus.CREATED, {'user': user})
            except PermissionError as error:
                self.send_json(HTTPStatus.FORBIDDEN, {'error': str(error)})
            except (ValueError, KeyError, json.JSONDecodeError) as error:
                self.send_json(HTTPStatus.BAD_REQUEST, {'error': str(error)})
            return
        if path != '/login':
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
        username = form.get('username', [''])[0].strip()
        password = form.get('password', [''])[0]
        if not self.state.verify_password(username, password):
            self.render_login(rd, '用户名或密码错误。', HTTPStatus.UNAUTHORIZED)
            return
        self.state.clear(key)
        value, expires = self.state.session_value(username)
        self.send_response(HTTPStatus.SEE_OTHER)
        self.send_header('Set-Cookie', f'{SESSION_COOKIE}={value}; Max-Age={expires-int(time.time())}; Path=/; HttpOnly; SameSite=Strict')
        self.send_header('Location', rd)
        self.send_header('Cache-Control', 'no-store')
        self.end_headers()

    def do_PATCH(self) -> None:
        actor = self.require_admin()
        username = self.user_path()
        if actor is None:
            return
        if username is None:
            self.send_error(HTTPStatus.NOT_FOUND)
            return
        try:
            user = self.state.update_user(str(actor['username']), username, self.read_json())
            self.send_json(HTTPStatus.OK, {'user': user})
        except PermissionError as error:
            self.send_json(HTTPStatus.FORBIDDEN, {'error': str(error)})
        except KeyError:
            self.send_json(HTTPStatus.NOT_FOUND, {'error': '用户不存在。'})
        except (ValueError, json.JSONDecodeError) as error:
            self.send_json(HTTPStatus.BAD_REQUEST, {'error': str(error)})

    def do_DELETE(self) -> None:
        actor = self.require_admin()
        username = self.user_path()
        if actor is None:
            return
        if username is None:
            self.send_error(HTTPStatus.NOT_FOUND)
            return
        try:
            self.state.delete_user(str(actor['username']), username)
            self.send_json(HTTPStatus.OK, {'deleted': username})
        except KeyError:
            self.send_json(HTTPStatus.NOT_FOUND, {'error': '用户不存在。'})
        except ValueError as error:
            self.send_json(HTTPStatus.BAD_REQUEST, {'error': str(error)})


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
