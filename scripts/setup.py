#!/usr/bin/env python3
"""Register existing native build tools in this project's ignored work directory."""
import argparse
import json
import os
from pathlib import Path
import shutil
import tempfile

from fn2auth_build.environment import validate
from fn2auth_build.inputs import ROOT, owned_directory


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    for name in ('node', 'npm', 'go', 'php', 'composer', 'playwright', 'chromium'):
        parser.add_argument('--' + name, type=Path, help='existing ' + name + ' executable/file')
    args = parser.parse_args()
    target = ROOT / '.build/environment.json'
    try:
        previous = json.loads(target.read_text()) if target.is_file() and not target.is_symlink() else {}
        settings = {}
        for name in ('node', 'npm', 'go', 'php', 'composer', 'playwright', 'chromium'):
            value = getattr(args, name) or previous.get(name) or shutil.which(name)
            if not value and name in ('playwright', 'chromium'):
                continue
            if not value:
                raise ValueError('provide --' + name + ' pointing to an existing native tool')
            settings[name] = str(Path(value).expanduser().absolute())
        versions = validate(settings)
        owned_directory(target.parent)
        if target.is_symlink():
            raise ValueError('build environment cannot be a symlink')
        with tempfile.NamedTemporaryFile('w', dir=target.parent, delete=False) as stream:
            pending = Path(stream.name)
            json.dump(settings, stream, indent=2)
            stream.write('\n')
            stream.flush()
            os.fsync(stream.fileno())
        os.replace(pending, target)
        print('Native build tools registered: ' + ', '.join(versions))
    except (OSError, ValueError, KeyError) as error:
        parser.exit(1, f'Fn2Auth setup: {error}\n')


if __name__ == '__main__':
    main()
