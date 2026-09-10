#!/usr/bin/env python3
"""Exercise deployment scripts in a temporary filesystem with fault-injected CLIs."""
import base64
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import sys
import tempfile
import unittest

SCRIPTS = Path(__file__).resolve().parent
MOCK = r'''
import hashlib, json, os, pathlib, shutil, sqlite3, subprocess, sys
p = pathlib.Path
root = p(os.environ['DEPLOY_TEST_ROOT'])
command = p(sys.argv[0]).name
args = sys.argv[1:]
with (root / 'commands').open('a') as log:
    log.write(json.dumps([command, *args]) + '\n')
state_path = root / 'services.json'
state = json.loads(state_path.read_text())
needle = os.environ.get('FAIL_COMMAND')
if needle and needle in ' '.join([command, *args]) and not (root / 'failed').exists():
    (root / 'failed').touch()
    sys.exit(1)
def save():
    state_path.write_text(json.dumps(state))
def ownership(path, owner):
    owners = json.loads((root / 'owners.json').read_text())
    owners[str(path)] = owner
    (root / 'owners.json').write_text(json.dumps(owners))
if command == 'id':
    print(0)
elif command == 'flock':
    pass
elif command == 'setfacl':
    pass
elif command == 'sleep':
    pass
elif command == 'timeout':
    sys.exit(subprocess.run(args[1:], env=os.environ).returncode)
elif command == 'findmnt':
    sys.exit(0 if (root / 'mount-present').exists() else 1)
elif command == 'umount':
    (root / 'mount-present').unlink(missing_ok=True)
    canary = root / 'srv/embymedia/data/clouddrive/CloudNAS/CloudDrive/.embymedia-health-canary'
    canary.unlink(missing_ok=True)
elif command == 'docker':
    canary = root / 'srv/embymedia/data/clouddrive/CloudNAS/CloudDrive/.embymedia-health-canary'
    if args[0] == 'inspect':
        print('healthy' if (root / 'cloud-healthy').exists() else os.environ.get('CLOUDDRIVE_HEALTH', 'healthy'))
    elif 'ps' in args and '-q' in args:
        print('cloud-container')
    elif 'up' in args:
        if 'clouddrive2' in args:
            canary.parent.mkdir(parents=True, exist_ok=True)
            canary.touch()
            (root / 'mount-present').touch()
            (root / 'cloud-healthy').touch()
    elif 'exec' in args:
        sys.exit(0 if canary.exists() else 1)
elif command == 'systemctl':
    op = args[0]
    units = [arg for arg in args[1:] if not arg.startswith('-')]
    if op == 'daemon-reload':
        sys.exit(0)
    if op == 'cat':
        sys.exit(0 if units[0] in state or (root / 'etc/systemd/system' / units[0]).exists() else 1)
    if op in ('is-active', 'is-enabled'):
        sys.exit(0 if state.get(units[0], {}).get('active' if op == 'is-active' else 'enabled') else 1)
    for unit in units:
        entry = state.setdefault(unit, {'active': False, 'enabled': False})
        if op in ('start', 'restart', 'stop'):
            entry['active'] = op != 'stop'
        if op in ('enable', 'disable'):
            entry['enabled'] = op == 'enable'
            if '--now' in args:
                entry['active'] = op == 'enable'
    save()
elif command == 'install':
    owner, mode, directory, paths = None, 0o755, False, []
    i = 0
    while i < len(args):
        if args[i] in ('-o', '-g', '-m'):
            if args[i] == '-o': owner = args[i + 1]
            if args[i] == '-m': mode = int(args[i + 1], 8)
            i += 2
        elif args[i] == '-d':
            directory = True
            i += 1
        else:
            paths.append(p(args[i]))
            i += 1
    if directory:
        for path in paths:
            path.mkdir(parents=True, exist_ok=True)
            path.chmod(mode)
            ownership(path, owner)
    else:
        for source in paths[:-1]:
            target = paths[-1] / source.name if paths[-1].is_dir() else paths[-1]
            shutil.copyfile(source, target)
            target.chmod(mode)
            ownership(target, owner)
elif command == 'chown':
    operands = [arg for arg in args if not arg.startswith('-')]
    for path in operands[1:]: ownership(path, operands[0].split(':')[0])
elif command == 'mv':
    source, target = map(p, args[-2:])
    if os.environ.get('FAIL_ROLLBACK') and source.is_symlink() and source.resolve().name == 'old':
        sys.exit(1)
    os.replace(source, target)
elif command == 'sha256sum':
    digest, filename = p(args[-1]).read_text().split()
    sys.exit(0 if hashlib.sha256(p(filename).read_bytes()).hexdigest() == digest else 1)
elif command == 'runuser':
    assert args[:3] == ['-u', 'embymedia', '--'], args
    sys.exit(subprocess.run(args[3:], env={**os.environ, 'FAKE_USER': 'embymedia'}).returncode)
elif command in ('embymedia', 'embymedia-linux-amd64'):
    database = p(args[args.index('-db') + 1])
    if 'restore-' not in str(database):
        assert os.environ.get('FAKE_USER') == 'embymedia', 'database process must run as service account'
        owners = json.loads((root / 'owners.json').read_text())
        assert owners[str(database.parent)] == 'embymedia', 'database parent must be service-owned'
        assert database.parent.stat().st_mode & 0o700 == 0o700
    if '-bootstrap-webhook-secret-file' in args:
        secret = p(args[args.index('-bootstrap-webhook-secret-file') + 1])
        assert secret.read_text().strip() == 'webhook-secret'
        assert secret.stat().st_mode & 0o777 == 0o600
    with sqlite3.connect(database) as connection:
        connection.execute('create table if not exists deployment_probe (value text)')
    ownership(database, os.environ.get('FAKE_USER', 'root'))
elif command == 'curl':
    current = root / 'opt/embymedia-v2/current'
    if os.environ.get('FAIL_HEALTH') and current.resolve().name == 'new': sys.exit(1)
    assert (current / 'bin/embymedia').is_file()
    service = 'embymedia-http-login.service' if ':9092/' in args[-1] else 'embymedia-v2.service'
    if '/api/v1/async-tasks?status=running' in args[-1]:
        polls = root / 'active-task-polls'
        if not polls.exists(): polls.write_text(os.environ.get('ACTIVE_TASK_POLLS', '0'))
        remaining = int(polls.read_text())
        polls.write_text(str(max(0, remaining - 1)))
        print('{"tasks":[{"id":"running"}]}' if remaining else '{"tasks":[]}')
    sys.exit(0 if state[service]['active'] else 1)
elif command == 'restic':
    if 'backup' in args:
        snapshot = root / 'snapshot'
        for arg in args:
            source = p(arg)
            if source.is_absolute() and source.exists() and str(source).startswith(str(root / 'srv/embymedia')):
                target = snapshot / source.relative_to(root)
                target.parent.mkdir(parents=True, exist_ok=True)
                if source.is_dir(): shutil.copytree(source, target, dirs_exist_ok=True)
                else: shutil.copy2(source, target)
    elif 'restore' in args:
        target = p(args[args.index('--target') + 1])
        shutil.copytree(root / 'snapshot', target, dirs_exist_ok=True)
    elif 'init' in args:
        p(args[args.index('--repo') + 1]).mkdir(parents=True)
else:
    raise SystemExit('unexpected mock command: ' + command)
'''


class DeploymentScriptsTest(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name).resolve()
        self.env = {**os.environ, 'DEPLOY_TEST_ROOT': str(self.root), 'EMBYMEDIA_SNAPSHOT_SOURCE_ROOT': str(self.root)}
        self.fake_bin = self.root / 'cli'
        self.fake_bin.mkdir()
        shim = self.fake_bin / 'shim'
        shim.write_text(f'#!{sys.executable}\n' + MOCK)
        shim.chmod(0o755)
        for name in ('id', 'flock', 'setfacl', 'sleep', 'timeout', 'findmnt', 'umount', 'docker', 'systemctl', 'install', 'chown', 'mv', 'sha256sum', 'runuser', 'curl', 'restic'):
            (self.fake_bin / name).symlink_to(shim)
        self.env['PATH'] = str(self.fake_bin) + os.pathsep + os.environ['PATH']
        self.source = self.root / 'source'
        shutil.copytree(SCRIPTS.parent, self.source / 'deploy')
        (self.source / 'bin').mkdir()
        binary = self.source / 'bin/embymedia-linux-amd64'
        shutil.copy2(shim, binary)
        binary.with_suffix('.sha256').write_text(hashlib.sha256(binary.read_bytes()).hexdigest() + '  ' + binary.name)
        self.old = self.root / 'opt/embymedia-v2/releases/old'
        (self.old / 'bin').mkdir(parents=True)
        shutil.copy2(shim, self.old / 'bin/embymedia')
        shutil.copytree(SCRIPTS.parent, self.old / 'deploy')
        self.current = self.old.parent.parent / 'current'
        self.current.symlink_to(self.old)
        self.initial_configs = {}
        for file in (self.source / 'deploy/systemd').iterdir():
            target = self.root / 'etc/systemd/system' / file.name
            target.parent.mkdir(parents=True, exist_ok=True)
            target.write_text('old ' + file.name)
            self.initial_configs[target] = target.read_bytes()
        for filename in ('etc/caddy/Caddyfile', 'etc/systemd/system/caddy.service.d/embymedia-login.conf', 'home/gaotao/.hermes/skills/embymedia-v2-operator/SKILL.md'):
            target = self.root / filename
            target.parent.mkdir(parents=True, exist_ok=True)
            target.write_text('old configuration')
            self.initial_configs[target] = target.read_bytes()
        stack_env = self.root / 'etc/embymedia/stack.env'
        stack_env.parent.mkdir(parents=True, exist_ok=True)
        stack_env.write_text('MEDIA_UID=1026\nMEDIA_GID=100\n')
        self.initial_configs[stack_env] = stack_env.read_bytes()
        (self.root / 'run/lock').mkdir(parents=True)
        secrets = self.root / 'etc/embymedia/secrets'
        secrets.mkdir(parents=True)
        (secrets / 'clouddrive-webhook-secret').write_text('webhook-secret\n')
        (secrets / 'restic-local-password').write_text('backup-secret')
        self.login = self.root / 'srv/embymedia/data/auth/http-login.json'
        self.login.parent.mkdir(parents=True)
        encoded = lambda value: base64.urlsafe_b64encode(value).decode().rstrip('=')
        self.identity = {'version': 2, 'sessionSecret': encoded(b's' * 32), 'users': [
            {'username': 'administrator', 'role': 'admin', 'enabled': True, 'passwordSalt': encoded(b'a' * 24),
             'passwordHash': encoded(b'h' * 32), 'iterations': 600000, 'sessionVersion': 'persistent-session-version'}]}
        self.login.write_text(json.dumps(self.identity))
        self.login.chmod(0o600)
        for folder in ('emby/config', 'clouddrive/config', 'strm-v2'):
            (self.login.parent.parent / folder).mkdir(parents=True)
        self.initial_services = {name: {'active': True, 'enabled': True} for name in (
            'docker.service', 'embymedia-v2.service', 'embymedia-http-login.service', 'embymedia-stack.service', 'caddy.service', 'embymedia-backup.timer')}
        (self.root / 'services.json').write_text(json.dumps(self.initial_services))
        (self.root / 'owners.json').write_text('{}')

    def run_script(self, name, *args, **env):
        text = (SCRIPTS / name).read_text()
        text = text.replace('/usr/bin/python3', sys.executable)
        # Only relocate absolute host roots; paths appended to $target remain archive-relative.
        for prefix in ('/opt/embymedia-v2', '/srv/embymedia', '/etc/embymedia', '/etc/systemd', '/etc/caddy', '/home/gaotao', '/run/lock', '/run'):
            text = re.sub(r'(?<![A-Za-z0-9_$])' + re.escape(prefix), str(self.root) + prefix, text)
        path = self.root / name
        path.write_text(text)
        return subprocess.run(['/bin/sh', str(path), *map(str, args)], env={**self.env, **env}, text=True, capture_output=True, timeout=30)

    def install(self, **env):
        return self.run_script('install-release.sh', self.source, 'new', **env)

    def assert_recovered(self, result):
        self.assertNotEqual(result.returncode, 0, result.stdout)
        self.assertEqual(self.current.resolve(), self.old)
        self.assertTrue((self.current / 'bin/embymedia').is_file())
        for path, content in self.initial_configs.items():
            self.assertEqual(path.read_bytes(), content)
        services = json.loads((self.root / 'services.json').read_text())
        for name, state in self.initial_services.items():
            self.assertEqual(services[name], state, name)
        self.assertEqual(json.loads(self.login.read_text()), self.identity)
        self.assertFalse((self.login.parent.parent / 'clouddrive/config/webhooks/webhook.toml').exists())

    def test_fresh_database_uses_service_identity(self):
        self.current.unlink()
        shutil.rmtree(self.old.parent)
        lock = self.login.parent.parent / 'embymedia.db.owner.lock'
        lock.write_bytes(b'')
        (self.root / 'owners.json').write_text(json.dumps({str(lock): 'root'}))
        (self.root / 'services.json').write_text('{}')
        result = self.install()
        self.assertEqual(result.returncode, 0, result.stderr)
        database = self.login.parent.parent / 'embymedia.db'
        self.assertEqual(json.loads((self.root / 'owners.json').read_text())[str(database)], 'embymedia')
        self.assertEqual(database.stat().st_mode & 0o777, 0o600)
        self.assertEqual(json.loads((self.root / 'owners.json').read_text())[str(lock)], 'embymedia')
        self.assertEqual(lock.stat().st_mode & 0o777, 0o600)
        self.assertEqual(self.current.resolve().parent.stat().st_mode & 0o005, 0o005)
        self.assertEqual(self.current.resolve().name, 'new')
        self.assertEqual(list(database.parent.glob('.webhook-secret-*')), [])
        webhook = self.login.parent.parent / 'clouddrive/config/webhooks/webhook.toml'
        self.assertEqual(webhook.read_text(), '[file_system_watcher]\nenabled = true\nurl = "http://host.docker.internal/hooks/clouddrive2?key=webhook-secret"\nmethod = "POST"\n')
        self.assertEqual(webhook.stat().st_mode & 0o777, 0o600)
        caddy_override = self.root / 'etc/systemd/system/caddy.service.d/embymedia-login.conf'
        self.assertEqual(caddy_override.read_text(), (self.source / 'deploy/caddy/embymedia-login.conf').read_text())
        commands = [json.loads(line) for line in (self.root / 'commands').read_text().splitlines()]
        self.assertTrue(any(command[:3] == ['systemctl', 'start', 'caddy.service'] for command in commands))
        self.assertFalse(any(command[:3] == ['systemctl', 'restart', 'caddy.service'] for command in commands))

    def test_existing_caddy_is_reloaded_without_restart(self):
        result = self.install()
        self.assertEqual(result.returncode, 0, result.stderr)
        commands = [json.loads(line) for line in (self.root / 'commands').read_text().splitlines()]
        self.assertTrue(any(command[:3] == ['systemctl', 'reload', 'caddy.service'] for command in commands))
        self.assertFalse(any(command[:3] == ['systemctl', 'restart', 'caddy.service'] for command in commands))

    def test_unchanged_webhook_configuration_is_not_replaced(self):
        webhook = self.login.parent.parent / 'clouddrive/config/webhooks/webhook.toml'
        webhook.parent.mkdir(parents=True, exist_ok=True)
        webhook.write_text('[file_system_watcher]\nenabled = false\nurl = "http://host.docker.internal/hooks/clouddrive2?key=webhook-secret"\nmethod = "POST"\n')
        inode = webhook.stat().st_ino
        result = self.install()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn('enabled = false\n', webhook.read_text())
        self.assertEqual(webhook.stat().st_ino, inode)

    def test_active_service_is_started_once_after_task_drain(self):
        result = self.install(ACTIVE_TASK_POLLS='1')
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn('waiting for active EmbyMedia task before deployment', result.stdout)
        commands = [json.loads(line) for line in (self.root / 'commands').read_text().splitlines()]
        task_checks = [i for i, command in enumerate(commands) if '/api/v1/async-tasks?status=running' in command[-1]]
        service_stop = next(i for i, command in enumerate(commands) if command[:3] == ['systemctl', 'stop', 'embymedia-v2.service'])
        starts = [command for command in commands if command[:3] in (['systemctl', 'start', 'embymedia-v2.service'], ['systemctl', 'restart', 'embymedia-v2.service'])]
        self.assertEqual(len(task_checks), 2)
        self.assertLess(task_checks[-1], service_stop)
        self.assertEqual(starts, [['systemctl', 'start', 'embymedia-v2.service']])

    def test_explicit_force_skips_active_task_drain(self):
        result = self.install(ACTIVE_TASK_POLLS='1', EMBYMEDIA_DEPLOY_FORCE='1')
        self.assertEqual(result.returncode, 0, result.stderr)
        commands = [json.loads(line) for line in (self.root / 'commands').read_text().splitlines()]
        self.assertFalse(any('/api/v1/async-tasks?status=running' in command[-1] for command in commands))
        self.assertTrue(any(command[:3] == ['systemctl', 'stop', 'embymedia-v2.service'] for command in commands))

    def test_successful_release_disables_retired_services(self):
        retired = ('embymedia-dsh.service', 'embymedia-control-helper.service')
        (self.root / 'services.json').write_text(json.dumps({
            **self.initial_services,
            **{name: {'active': True, 'enabled': True} for name in retired},
        }))
        result = self.install()
        self.assertEqual(result.returncode, 0, result.stderr)
        services = json.loads((self.root / 'services.json').read_text())
        for name in retired:
            self.assertEqual(services[name], {'active': False, 'enabled': False})

    def test_retired_services_stay_disabled_after_release_rollback(self):
        retired = ('embymedia-dsh.service', 'embymedia-control-helper.service')
        (self.root / 'services.json').write_text(json.dumps({
            **self.initial_services,
            **{name: {'active': True, 'enabled': True} for name in retired},
        }))
        self.assert_recovered(self.install(FAIL_COMMAND='systemctl reload caddy.service'))
        services = json.loads((self.root / 'services.json').read_text())
        for name in retired:
            self.assertEqual(services[name], {'active': False, 'enabled': False})

    def test_retired_service_disable_failure_leaves_current_release_running(self):
        retired = 'embymedia-dsh.service'
        state = {**self.initial_services, retired: {'active': True, 'enabled': True}}
        (self.root / 'services.json').write_text(json.dumps(state))
        result = self.install(FAIL_COMMAND=f'systemctl disable --now {retired}')
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(self.current.resolve(), self.old)
        self.assertEqual(json.loads((self.root / 'services.json').read_text()), state)
        for path, content in self.initial_configs.items():
            self.assertEqual(path.read_bytes(), content)

    def test_database_failure_restarts_previous_service(self):
        self.assert_recovered(self.install(FAIL_COMMAND='embymedia -check-db'))

    def test_late_caddy_failure_restores_binary_and_configs(self):
        self.assert_recovered(self.install(FAIL_COMMAND='systemctl reload caddy.service'))

    def test_configuration_install_failure_restores_previous_deployment(self):
        self.assert_recovered(self.install(FAIL_COMMAND='install -o gaotao -g gaotao -m 0644'))

    def test_daemon_reload_failure_restores_previous_deployment(self):
        self.assert_recovered(self.install(FAIL_COMMAND='systemctl daemon-reload'))

    def test_startup_failure_restores_previous_deployment(self):
        self.assert_recovered(self.install(FAIL_COMMAND='systemctl start embymedia-v2.service'))

    def test_unhealthy_release_restores_previous_deployment(self):
        self.assert_recovered(self.install(FAIL_HEALTH='1'))

    def test_failed_symlink_rollback_retains_active_binary(self):
        result = self.install(FAIL_COMMAND='systemctl reload caddy.service', FAIL_ROLLBACK='1')
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(self.current.resolve().name, 'new')
        self.assertTrue((self.current / 'bin/embymedia').is_file())
        self.assertIn('deployment recovery failed', result.stderr)
        self.assertTrue(list(self.current.parent.glob('.install-*')))

    def test_aliased_release_root_retains_active_binary_on_failed_rollback(self):
        alias = self.root / 'alias'
        alias.symlink_to(self.root, target_is_directory=True)
        self.root = alias
        result = self.install(FAIL_COMMAND='systemctl reload caddy.service', FAIL_ROLLBACK='1')
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(self.current.resolve().name, 'new')
        self.assertTrue((self.current / 'bin/embymedia').is_file())
        self.assertIn('deployment recovery failed', result.stderr)

    def test_first_install_failure_preserves_identity_without_dangling_link(self):
        self.current.unlink()
        (self.root / 'services.json').write_text('{}')
        result = self.install(FAIL_COMMAND='systemctl start caddy.service')
        self.assertNotEqual(result.returncode, 0)
        self.assertFalse(self.current.is_symlink())
        self.assertEqual(json.loads(self.login.read_text()), self.identity)
        self.assertFalse(json.loads((self.root / 'services.json').read_text())['embymedia-v2.service']['active'])

    def test_clouddrive_recovery_replaces_stale_mount_and_restores_v2(self):
        (self.root / 'mount-present').touch()
        result = self.run_script('clouddrive-recover.sh', CLOUDDRIVE_HEALTH='unhealthy')
        self.assertEqual(result.returncode, 0, result.stderr)
        canary = self.root / 'srv/embymedia/data/clouddrive/CloudNAS/CloudDrive/.embymedia-health-canary'
        self.assertTrue(canary.is_file())
        self.assertTrue(json.loads((self.root / 'services.json').read_text())['embymedia-v2.service']['active'])
        commands = [json.loads(line) for line in (self.root / 'commands').read_text().splitlines()]
        self.assertTrue(any(command[0] == 'umount' and '-l' in command for command in commands))
        self.assertTrue(any(command[0] == 'docker' and 'stop' in command and 'clouddrive2' in command for command in commands))
        self.assertGreaterEqual(sum(command[0] == 'docker' and 'up' in command for command in commands), 2)

    def test_stack_stop_cleans_remaining_fuse_mount(self):
        (self.root / 'mount-present').touch()
        result = self.run_script('clouddrive-recover.sh', 'stop-stack')
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertFalse((self.root / 'mount-present').exists())
        commands = [json.loads(line) for line in (self.root / 'commands').read_text().splitlines()]
        docker_stop = next(index for index, command in enumerate(commands) if command[0] == 'docker' and 'stop' in command)
        unmount = next(index for index, command in enumerate(commands) if command[0] == 'umount')
        self.assertLess(docker_stop, unmount)

    def test_backup_restores_live_browser_identity(self):
        database = self.login.parent.parent / 'embymedia.db'
        database.write_bytes(b'')
        result = self.run_script('backup.sh')
        self.assertEqual(result.returncode, 0, result.stderr)
        services = json.loads((self.root / 'services.json').read_text())
        self.assertTrue(all(service['active'] for service in services.values()))
        commands = [json.loads(line) for line in (self.root / 'commands').read_text().splitlines()]
        self.assertFalse(any(command[:2] == ['systemctl', 'stop'] for command in commands))
        self.assertFalse((self.root / 'srv/embymedia/backups/.online-snapshot').exists())
        target = self.root / 'srv/embymedia/restore-roundtrip'
        result = self.run_script('restore-isolated.sh', 'latest', target)
        self.assertEqual(result.returncode, 0, result.stderr)
        restored = target / 'srv/embymedia/data/auth/http-login.json'
        self.assertEqual(json.loads(restored.read_text()), self.identity)
        self.assertEqual(restored.stat().st_mode & 0o777, 0o600)
        self.assertEqual(target.stat().st_mode & 0o777, 0o700)
        self.assertEqual(database.read_bytes(), b'')

    def test_restore_creates_missing_normalized_strm_root(self):
        snapshot = self.root / 'snapshot/srv/embymedia/data'
        shutil.copytree(self.login.parent.parent, snapshot)
        shutil.rmtree(snapshot / 'strm-v2')
        (snapshot / 'embymedia.db').write_bytes(b'')
        target = self.root / 'srv/embymedia/restore-before-strm-v2'
        result = self.run_script('restore-isolated.sh', 'latest', target)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertTrue((target / 'srv/embymedia/data/strm-v2').is_dir())

    def test_restore_rejects_missing_or_corrupt_browser_auth(self):
        snapshot = self.root / 'snapshot/srv/embymedia/data'
        shutil.copytree(self.login.parent.parent, snapshot)
        (snapshot / 'embymedia.db').write_bytes(b'')
        for index, content in enumerate((None, '{}', json.dumps({**self.identity, 'users': []}))):
            with self.subTest(content=content):
                auth = snapshot / 'auth/http-login.json'
                if content is None: auth.unlink()
                else: auth.write_text(content)
                target = self.root / f'srv/embymedia/restore-invalid-{index}'
                result = self.run_script('restore-isolated.sh', 'latest', target)
                self.assertNotEqual(result.returncode, 0)
                self.assertEqual((target / 'srv/embymedia/data/embymedia.db').read_bytes(), b'')

    def test_restore_rejects_parent_traversal_before_restic(self):
        result = self.run_script('restore-isolated.sh', 'latest', self.root / 'srv/embymedia/restore-a/../data')
        self.assertNotEqual(result.returncode, 0)
        self.assertFalse((self.root / 'commands').exists())


if __name__ == '__main__':
    unittest.main()
