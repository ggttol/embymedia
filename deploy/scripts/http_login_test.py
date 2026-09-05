import hashlib
import hmac
import importlib.util
import json
import pathlib
import tempfile
import unittest


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
            self.assertTrue(state.verify_password('gaotao', 'administrator-password'))
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
            self.assertTrue(state.verify_password('family', 'family-password'))
            session, _ = state.session_value('family')
            self.assertIsNotNone(state.session_user(session))

            updated = state.update_user('gaotao', 'family', {'role': 'admin', 'enabled': True, 'password': 'replacement-password'})
            self.assertEqual(updated['role'], 'admin')
            self.assertFalse(state.verify_password('family', 'family-password'))
            self.assertTrue(state.verify_password('family', 'replacement-password'))
            self.assertIsNone(state.session_user(session))

            with self.assertRaisesRegex(ValueError, '当前管理员'):
                state.update_user('gaotao', 'gaotao', {'enabled': False})
            with self.assertRaisesRegex(ValueError, '当前登录用户'):
                state.delete_user('gaotao', 'gaotao')

            state.delete_user('gaotao', 'family')
            self.assertEqual([user['username'] for user in state.list_users()], ['gaotao'])
            reloaded = LOGIN.LoginState(str(path))
            self.assertEqual([user['username'] for user in reloaded.list_users()], ['gaotao'])


if __name__ == '__main__':
    unittest.main()
