import hashlib
import hmac
import importlib.util
import io
import json
import pathlib
import tempfile
import threading
import unittest
from concurrent.futures import ThreadPoolExecutor
from http.client import HTTPResponse
from types import SimpleNamespace
from unittest.mock import Mock, patch


MODULE_PATH = pathlib.Path(__file__).with_name('http-login.py')
SPEC = importlib.util.spec_from_file_location('embymedia_http_login', MODULE_PATH)
assert SPEC and SPEC.loader
LOGIN = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(LOGIN)


class LoginStateTest(unittest.TestCase):
    def legacy_config(self, path: pathlib.Path) -> None:
        password = LOGIN.password_fields('administrator-password')
        config = {
            'username': 'gaotao',
            **password,
            'sessionSecret': LOGIN.b64encode(b's' * 32),
            'sessionDays': 30,
            'authority': 'gaotao.cc:3080',
        }
        path.write_text(json.dumps(config))

    def test_migrates_legacy_admin_and_accepts_existing_session(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            path = pathlib.Path(directory) / 'login.json'
            self.legacy_config(path)
            state = LOGIN.LoginState(str(path))
            self.assertIsNotNone(state.authenticate('gaotao', 'administrator-password'))
            self.assertEqual(state.list_users()[0]['role'], 'admin')
            migrated = json.loads(path.read_text())
            self.assertEqual(migrated['version'], 2)
            self.assertNotIn('passwordHash', state.list_users()[0])

            now = int(LOGIN.time.time())
            body = LOGIN.b64encode(json.dumps({'v': 1, 'u': 'gaotao', 'iat': now, 'exp': now + 60}, separators=(',', ':')).encode())
            signature = LOGIN.b64encode(hmac.new(state.session_secret, body.encode(), hashlib.sha256).digest())
            user = state.session_user(f'{body}.{signature}')
            self.assertIsNotNone(user)
            self.assertEqual(user['username'], 'gaotao')

    def test_user_lifecycle_and_session_invalidation(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            path = pathlib.Path(directory) / 'login.json'
            self.legacy_config(path)
            state = LOGIN.LoginState(str(path))
            created = state.create_user('family', 'family-password', 'operator')
            self.assertEqual(created['role'], 'operator')
            authentication = state.authenticate('family', 'family-password')
            self.assertIsNotNone(authentication)
            session, _ = authentication
            self.assertIsNotNone(state.session_user(session))

            updated = state.update_user('gaotao', 'family', {'role': 'admin', 'enabled': True, 'password': 'replacement-password'})
            self.assertEqual(updated['role'], 'admin')
            self.assertIsNone(state.authenticate('family', 'family-password'))
            self.assertIsNotNone(state.authenticate('family', 'replacement-password'))
            self.assertIsNone(state.session_user(session))

            with self.assertRaises(ValueError):
                state.update_user('gaotao', 'gaotao', {'enabled': False})
            with self.assertRaises(ValueError):
                state.delete_user('gaotao', 'gaotao')

            state.delete_user('gaotao', 'family')
            self.assertEqual([user['username'] for user in state.list_users()], ['gaotao'])
            reloaded = LOGIN.LoginState(str(path))
            self.assertEqual([user['username'] for user in reloaded.list_users()], ['gaotao'])

    def request(self, state, method, path, body, headers):
        request = f'{method} {path} HTTP/1.0\r\nContent-Length: {len(body)}\r\n'
        request += ''.join(f'{name}: {value}\r\n' for name, value in headers.items())
        output = io.BytesIO()
        connection = Mock()
        connection.makefile.return_value = io.BytesIO(request.encode() + b'\r\n' + body)
        connection.sendall.side_effect = output.write
        LOGIN.LoginHandler(connection, ('127.0.0.1', 12345), SimpleNamespace(state=state))
        response_connection = Mock()
        response_connection.makefile.return_value = io.BytesIO(output.getvalue())
        response = HTTPResponse(response_connection)
        response.begin()
        self.addCleanup(response.close)
        return response

    def test_login_rejects_credentials_changed_during_password_verification(self) -> None:
        for change in ('reset', 'disable', 'delete'):
            with self.subTest(change=change), tempfile.TemporaryDirectory() as directory:
                path = pathlib.Path(directory) / 'login.json'
                self.legacy_config(path)
                state = LOGIN.LoginState(str(path))
                state.create_user('family', 'family-password', 'operator')
                verified = threading.Event()
                resume = threading.Event()
                derive = LOGIN.hashlib.pbkdf2_hmac

                def paused_derive(algorithm, password, salt, iterations):
                    result = derive(algorithm, password, salt, iterations)
                    if password == b'family-password':
                        verified.set()
                        if not resume.wait(10):
                            raise TimeoutError('password verification was not resumed')
                    return result

                body = LOGIN.urllib.parse.urlencode({
                    'username': 'family', 'password': 'family-password', 'csrf': 'test-csrf',
                }).encode()
                headers = {'Cookie': f'{LOGIN.CSRF_COOKIE}=test-csrf'}
                with patch.object(LOGIN.hashlib, 'pbkdf2_hmac', side_effect=paused_derive), ThreadPoolExecutor(max_workers=1) as executor:
                    login = executor.submit(self.request, state, 'POST', '/login', body, headers)
                    try:
                        self.assertTrue(verified.wait(10), 'login did not reach password verification')
                        if change == 'reset':
                            state.update_user('gaotao', 'family', {'password': 'replacement-password'})
                        elif change == 'disable':
                            state.update_user('gaotao', 'family', {'enabled': False})
                        else:
                            state.delete_user('gaotao', 'family')
                    finally:
                        resume.set()
                    response = login.result(timeout=10)
                self.assertEqual(response.status, 401)
                self.assertFalse(any(
                    value.startswith(f'{LOGIN.SESSION_COOKIE}=')
                    for name, value in response.getheaders() if name.lower() == 'set-cookie'
                ))

    def test_invalid_password_update_preserves_live_and_stored_user(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            path = pathlib.Path(directory) / 'login.json'
            self.legacy_config(path)
            state = LOGIN.LoginState(str(path))
            state.create_user('family', 'family-password', 'operator')
            admin_session, _ = state.authenticate('gaotao', 'administrator-password')
            family_session, _ = state.authenticate('family', 'family-password')
            before_users = state.list_users()
            before_config = path.read_bytes()
            response = self.request(state, 'PATCH', '/auth/users/family', json.dumps({
                'role': 'admin', 'enabled': False, 'password': 'short',
            }).encode(), {
                'Content-Type': 'application/json',
                'Cookie': f'{LOGIN.SESSION_COOKIE}={admin_session}',
            })
            self.assertEqual(response.status, 400)
            self.assertEqual(state.list_users(), before_users)
            self.assertEqual(path.read_bytes(), before_config)
            self.assertEqual(LOGIN.LoginState(str(path)).list_users(), before_users)
            self.assertEqual(state.session_user(family_session)['role'], 'operator')

    def test_failed_persistence_does_not_publish_user_update(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            path = pathlib.Path(directory) / 'login.json'
            self.legacy_config(path)
            state = LOGIN.LoginState(str(path))
            state.create_user('family', 'family-password', 'operator')
            session, _ = state.authenticate('family', 'family-password')
            before_users = state.list_users()
            before_config = path.read_bytes()
            with patch.object(LOGIN.os, 'replace', side_effect=OSError('disk failure')):
                with self.assertRaises(OSError):
                    state.update_user('gaotao', 'family', {
                        'role': 'admin', 'enabled': False, 'password': 'replacement-password',
                    })
            self.assertEqual(state.list_users(), before_users)
            self.assertEqual(path.read_bytes(), before_config)
            self.assertEqual(LOGIN.LoginState(str(path)).list_users(), before_users)
            self.assertEqual(state.session_user(session)['role'], 'operator')
            self.assertIsNotNone(state.authenticate('family', 'family-password'))
            self.assertIsNone(state.authenticate('family', 'replacement-password'))


if __name__ == '__main__':
    unittest.main()
