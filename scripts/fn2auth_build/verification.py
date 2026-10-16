"""Verify actual package lifecycle phases, then reuse native PHP for security checks."""
import json
import os
import pwd
import subprocess
from pathlib import Path

from buildflow.privilege import require_sudo
from fnpackkit.execution_directory import execution_directory
from fnpackkit.lifecycle_verify import LifecycleFixture
from .inputs import ROOT, native, write_json


def verify(ctx, artifacts, settings, environment, modfile):
    package = next((Path(item.path) for item in artifacts if str(item.path).endswith('_amd64.fpk')), None)
    if package is None:
        raise ValueError('actual native package verification requires the amd64 target')
    lifecycle(ctx, package, environment)
    fixture = execution_directory()
    model = LifecycleFixture(package, fixture)
    model.deploy()
    app = model.app
    for name in ('state', 'run'):
        (fixture / name).mkdir(mode=0o700)
    configuration = json.loads((app / 'etc/fn2auth.json').read_text())
    configuration.update(app_dir=str(app), state_dir=str(fixture / 'state'), run_dir=str(fixture / 'run'))
    write_json(fixture / 'config.json', configuration)
    write_json(fixture / 'fixture.json', {'schema': 1, 'package': str(package),
                                         'scope': 'actual FPK; synthetic NAS business identities'})
    write_json(ctx.work / 'reports/native-fixture.json', {'path': str(fixture)})
    env = {**environment, 'FN2AUTH_SECURITY_FIXTURE': str(fixture),
           'FN2AUTH_SECURITY_EVIDENCE': str(ctx.work / 'reports/security'),
           'FN2AUTH_SECURITY_EXPIRY': '1'}
    with ctx.activity('验证实际 FPK 的原生隔离与业务边界'):
        native(ctx, [settings['go'], 'test', '-modfile=' + str(modfile), './tests/security',
                     '-count=1', '-timeout=10m'], ROOT, env)
    write_json(ctx.work / 'reports/verification-scope.json', {
        'amd64': 'actual package file ordering, native lifecycle, PHP/security and browser',
        'arm64': 'compiled Go; checksum/ELF checked PHP; no ARM device execution',
        'fnos_devices': 'no package-manager installation, mobile camera or FN Connect'})
    return fixture


def lifecycle(ctx, package, environment):
    """Execute lifecycle with a root namespace and nonroot application identity."""
    if os.geteuid() == 0:
        raise ValueError('normal application build must use a nonroot account')
    require_sudo(ctx, '隔离生命周期验证')
    username = pwd.getpwuid(os.geteuid()).pw_name
    toolkit = Path(os.environ['FNPACKKIT_PATH']) / 'src'
    engine = Path(os.environ['BUILDFLOW_PATH']) / 'src'
    command = ['sudo', '-n', '/usr/bin/python3', '-I',
                 str(ROOT / 'scripts/fn2auth_build/native_lifecycle.py'),
                 '--package', str(package), '--toolkit', str(toolkit), '--engine', str(engine),
                 '--username', username, '--version', ctx.project.data['version'],
                 '--evidence', str(ctx.work / 'reports/package-lifecycle.json')]
    # Preserve the authorized terminal's sudo ticket for this bounded root fixture.
    # The shared command runner intentionally creates a separate session.
    with ctx.activity('验证隔离目录中的 root 生命周期与应用降权'):
        result = subprocess.run(command, cwd=ROOT, env=environment, stdin=subprocess.DEVNULL,
                                capture_output=True, text=True, timeout=600)
        if result.returncode:
            raise ValueError('isolated lifecycle verification failed: ' + result.stderr[-1000:])
