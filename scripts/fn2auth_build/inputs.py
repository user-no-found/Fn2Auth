"""Build input hashing, private output paths and bounded native commands."""
import hashlib
import json
import os
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]


def digest(path):
    checksum = hashlib.sha256()
    with Path(path).open('rb') as stream:
        for chunk in iter(lambda: stream.read(1024 * 1024), b''):
            checksum.update(chunk)
    return checksum.hexdigest()


def write_json(path, value):
    path = Path(path)
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(json.dumps(value, ensure_ascii=False, indent=2) + '\n')


def owned_directory(path):
    """Reject links in project build/cache paths before creating them."""
    path = Path(path).absolute()
    relative = path.relative_to(ROOT)
    current = ROOT
    for part in relative.parts:
        current /= part
        if current.is_symlink():
            raise ValueError(f'build directory cannot be a symlink: {current}')
        current.mkdir(exist_ok=True)
    return path


def capped(argv):
    cpus = sorted(os.sched_getaffinity(0))
    count = min(8, max(1, len(cpus) // 2))
    return ['nice', '-n', '10', 'taskset', '-c', ','.join(map(str, cpus[:count])), *map(str, argv)]


def native(ctx, argv, cwd, environment, capture=False):
    """Run native build children with only the explicitly selected environment."""
    command = capped(['env', '-i', *(f'{key}={value}' for key, value in environment.items()), *argv])
    return (ctx.runner.capture if capture else ctx.runner.run)(command, cwd=cwd)


def project_digest():
    """Bind maintained application inputs while excluding generated/private trees."""
    checksum = hashlib.sha256()
    excluded = {'.git', '.build', '.cache', 'output', 'tasks', 'node_modules', '__pycache__'}
    for parent, directories, files in os.walk(ROOT, followlinks=False):
        directories[:] = sorted(item for item in directories if item not in excluded)
        for name in sorted(files):
            path = Path(parent) / name
            if name.endswith('.pyc'):
                continue
            if path.is_symlink():
                raise ValueError(f'application input cannot be a symlink: {path}')
            checksum.update(path.relative_to(ROOT).as_posix().encode() + b'\0')
            checksum.update(digest(path).encode() + b'\0')
    return checksum.hexdigest()
