#!/usr/bin/env python3
"""Stage an isolated native application fixture without installing an FPK or using real data."""
import argparse
import json
from pathlib import Path
import shutil

from fnpackkit.execution_directory import execution_directory
from fn2auth_build import backend
from fn2auth_build.inputs import ROOT, write_json


def stage(source, binary, destination):
    app = destination / 'app'
    app.mkdir()
    (app / 'bin').mkdir()
    (app / 'runtime').mkdir()
    shutil.copyfile(binary, app / 'bin/fn2auth')
    (app / 'bin/fn2auth').chmod(0o755)
    shutil.copyfile(ROOT / '.cache/native/amd64/frankenphp', app / 'runtime/frankenphp')
    (app / 'runtime/frankenphp').chmod(0o755)
    upstream = app / 'upstream/app'
    upstream.mkdir(parents=True)
    for name in ('app', 'bootstrap', 'config', 'database', 'lang', 'resources', 'public', 'vendor', 'routes'):
        if (source / name).is_dir():
            shutil.copytree(source / name, upstream / name, ignore=shutil.ignore_patterns('node_modules', '.git', 'hot'))
    for name in ('artisan', 'composer.json', 'composer.lock', 'LICENSE'):
        shutil.copyfile(source / name, upstream / name)
    if not (upstream / 'bootstrap/fn2auth-bootstrap.php').is_file():
        backend.apply(upstream, destination / 'reports')
    for name in ('state', 'run'):
        (destination / name).mkdir(mode=0o700)
    metadata = json.loads((ROOT / 'project.json').read_text())
    template = (ROOT / 'packaging/fnos/app/etc/fn2auth.json.in').read_text()
    for token, value in {'@app_dir@': str(app), '@state_dir@': str(destination / 'state'),
                         '@run_dir@': str(destination / 'run'), '@GATEWAY_PREFIX@': metadata['gateway_prefix']}.items():
        template = template.replace(token, value)
    (destination / 'config.json').write_text(template)
    write_json(destination / 'fixture.json', {'schema': 1, 'source': str(source),
               'scope': 'isolated native app; synthetic user tests do not prove real fnOS user isolation'})
    return destination


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--source', type=Path, required=True)
    parser.add_argument('--binary', type=Path, default=ROOT / '.build/bootstrap/fn2auth')
    args = parser.parse_args()
    destination = execution_directory()
    print(stage(args.source.resolve(strict=True), args.binary.resolve(strict=True), destination))
