"""Verify actual Fn2Auth FPK bytes on a disposable volume under a nonroot account."""
import argparse
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys


def verify(package, toolkit, engine, username, version, evidence):
    if os.geteuid() != 0:
        raise ValueError('explicit root fixture execution required')
    sys.path.insert(0, str(toolkit))
    sys.path.insert(0, str(engine))
    from fnpackkit.namespace_fixture import NamespaceFixture
    from fnpackkit.execution_directory import execution_directory
    root = execution_directory()
    model = NamespaceFixture(package, root, username)

    def run(argv, cwd, env):
        result = subprocess.run(argv, cwd=cwd, env={'PATH': '/usr/bin:/bin', **env},
                                capture_output=True, text=True, timeout=180)
        if result.returncode:
            raise RuntimeError(f'lifecycle command failed (exit {result.returncode}): '
                               + result.stderr[-800:])
        return result.stdout

    cleaned = False
    try:
        model.install(run)
        actual = run([str(model.app / 'bin/fn2auth'), 'version'], root, {}).strip()
        if actual != version:
            raise ValueError('actual FPK executable version mismatch')
        config = model.roots['etc'] / 'fn2auth.json'
        before = config.read_bytes()
        model.invoke('install_callback', run)
        for action in ('start', 'start', 'status', 'stop', 'stop'):
            model.invoke('main:' + action, run)
        for phase in ('config_init', 'config_callback'):
            model.invoke(phase, run)
        model.invoke('main:start', run)
        model.upgrade(run)
        user = model.roots['var'] / 'fn2auth/users/123'
        protected = ['database.sqlite', 'app.key', 'storage/oauth-private.key',
                     'storage/oauth-public.key', 'snapshots/backup/database.sqlite']
        transient = ['cache/compiled', 'tmp/pending', 'storage/logs/php.log',
                     'storage/framework/cache/file', 'storage/framework/views/view',
                     'storage/framework/sessions/session']
        for name in protected + transient:
            p = user / name
            p.parent.mkdir(parents=True, exist_ok=True)
            p.write_text('synthetic data ' + name)
            p.chmod(0o600)
        for p in [user, *user.rglob('*')]:
            os.chown(p, model.account.pw_uid, model.account.pw_gid)
            if p.is_dir():
                p.chmod(0o700)
        os.chown(user.parent, model.account.pw_uid, model.account.pw_gid)
        user.parent.chmod(0o700)
        model.uninstall(run)
        model.invoke('uninstall_callback', run)
        if config.read_bytes() != before or any(not (user / p).is_file() for p in protected):
            raise ValueError('retain lost configuration or synthetic protected bytes')
        if any((user / p).exists() for p in transient):
            raise ValueError('retain left declared disposable state')
        model.install(run)
        model.uninstall(run, {'wizard_data_action': 'delete'})
        model.invoke('uninstall_callback', run, {'wizard_data_action': 'delete'})
        if any(path.exists() for path in model.roots.values()):
            raise ValueError('complete deletion left a data root')
        model.install(run)
        result = subprocess.run([str(model.metadata / 'cmd/uninstall_init')],
                                env={**model.environment, 'PATH': '/usr/bin:/bin',
                                     'wizard_data_action': 'invalid'},
                                capture_output=True, text=True)
        if result.returncode == 0 or 'invalid_data_action' not in result.stderr:
            raise ValueError('invalid uninstall selection was not rejected')
        if not model.app.is_dir() or not config.is_file():
            raise ValueError('invalid uninstall changed current runtime/configuration')
        model.uninstall(run, {'wizard_data_action': 'delete'})
        if any(path.exists() for path in model.roots.values()):
            raise ValueError('fixture final delete incomplete')
        report = {'version': actual, 'events': model.events,
                  'checks': ['actual archive install/upgrade/start/status/stop',
                             'root lifecycle dispatch with nonroot app hooks',
                             'retain config/database/keys/snapshots; prune declared disposable state',
                             'post callback without runtime; complete five-root delete',
                             'repeat callbacks and invalid choice preserve runtime/state'],
                  'scope': 'root-owned disposable namespace; synthetic data; real nonroot OS account'}
        evidence.write_text(json.dumps(report, ensure_ascii=False, indent=2) + '\n')
        os.chown(evidence, model.account.pw_uid, model.account.pw_gid)
        evidence.chmod(0o600)
        cleaned = True
    finally:
        if model.app.is_dir() and (model.roots['etc'] / 'fn2auth.json').is_file():
            model.invoke('main:stop', run)
        if cleaned:
            shutil.rmtree(root)
        else:
            print('Failed fixture preserved: ' + str(root), file=sys.stderr)


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--package', type=Path, required=True)
    parser.add_argument('--toolkit', type=Path, required=True)
    parser.add_argument('--engine', type=Path, required=True)
    parser.add_argument('--username', required=True)
    parser.add_argument('--version', required=True)
    parser.add_argument('--evidence', type=Path, required=True)
    args = parser.parse_args()
    verify(args.package, args.toolkit, args.engine, args.username, args.version, args.evidence)
