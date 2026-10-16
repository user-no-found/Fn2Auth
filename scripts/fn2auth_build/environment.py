"""Load explicitly configured native tool locations without changing the host."""
import json
import os
from pathlib import Path
import subprocess

from .inputs import ROOT, digest


def validate(settings):
    lock = json.loads((ROOT / 'sources.lock.json').read_text())
    runtime = json.loads((ROOT / 'runtime.lock.json').read_text())
    for name in ('node', 'npm', 'go', 'php', 'composer'):
        path = Path(settings[name])
        if not path.is_absolute() or not path.is_file():
            raise ValueError(f'{name} must name an existing absolute tool path')
    def version(argv):
        return subprocess.check_output(argv, text=True, stderr=subprocess.STDOUT).strip()
    node = version([settings['node'], '--version'])
    if not node.startswith(f"v{lock['node_major']}."):
        raise ValueError('Node major version differs from sources.lock.json')
    go = version([settings['go'], 'version'])
    if not go.startswith('go version ' + lock['go_version'] + ' linux/amd64'):
        raise ValueError('Go toolchain differs from sources.lock.json')
    if digest(settings['php']) != runtime['frankenphp']['architectures']['amd64']['sha256']:
        raise ValueError('build PHP runtime checksum mismatch')
    if digest(settings['composer']) != runtime['composer']['sha256']:
        raise ValueError('Composer checksum mismatch')
    browser_tools = {}
    for name in ('playwright', 'chromium'):
        if name in settings and (not Path(settings[name]).is_absolute() or not Path(settings[name]).is_file()):
            raise ValueError(f'{name} must name an existing absolute tool file')
    if 'playwright' in settings:
        package = json.loads(Path(settings['playwright']).read_text())
        if package.get('name') != 'playwright':
            raise ValueError('playwright must point to its package.json')
        browser_tools['playwright'] = package['version']
    if 'chromium' in settings:
        browser_tools['chromium'] = version([settings['chromium'], '--version'])
    return {**browser_tools, 'node': node, 'npm': version([settings['npm'], '--version']), 'go': go,
            'php': version([settings['php'], 'version']),
            'composer': version([settings['php'], 'php-cli', settings['composer'], '--version'])}


def load():
    path = ROOT / '.build/environment.json'
    if path.is_symlink() or not path.is_file():
        raise ValueError('run ./setup.sh with the existing native tool paths first')
    settings = json.loads(path.read_text())
    required = {'node', 'npm', 'go', 'php', 'composer'}
    if not required.issubset(settings) or set(settings) - required - {'playwright', 'chromium'}:
        raise ValueError('invalid build environment keys')
    return settings, validate(settings)


def command_environment(settings, work):
    temporary = work / 'tmp'
    temporary.mkdir(parents=True, exist_ok=True)
    # Tools inherit no application credentials or platform API token.
    keep = ('HOME', 'LANG', 'LC_ALL', 'SSL_CERT_FILE', 'SSL_CERT_DIR')
    environment = {key: os.environ[key] for key in keep if key in os.environ}
    environment.update(PATH=str(Path(settings['node']).parent) + ':/usr/local/bin:/usr/bin:/bin',
                       TMPDIR=str(temporary), GOTOOLCHAIN='local', CGO_ENABLED='0', GOMAXPROCS='8')
    return environment
