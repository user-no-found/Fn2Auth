"""Obtain checksum-pinned native PHP payloads and their redistribution licenses."""
import json
import os
from pathlib import Path
import shutil
import tempfile
import urllib.request

from .inputs import ROOT, digest, owned_directory


def download(url, checksum, destination, existing=None):
    """Publish a download to its cache only after validating the complete bytes."""
    if destination.is_symlink():
        raise ValueError(f'cache file cannot be a symlink: {destination}')
    if destination.is_file() and digest(destination) == checksum:
        return destination
    owned_directory(destination.parent)
    with tempfile.NamedTemporaryFile(dir=destination.parent, delete=False) as output:
        pending = Path(output.name)
        try:
            if existing and digest(existing) == checksum:
                with Path(existing).open('rb') as source:
                    shutil.copyfileobj(source, output)
            else:
                with urllib.request.urlopen(url, timeout=60) as source:
                    shutil.copyfileobj(source, output)
            output.flush()
            os.fsync(output.fileno())
            if digest(pending) != checksum:
                raise ValueError(f'download checksum mismatch: {destination.name}')
            os.replace(pending, destination)
        finally:
            pending.unlink(missing_ok=True)
    return destination


def prepare(ctx, settings, architectures):
    lock = json.loads((ROOT / 'runtime.lock.json').read_text())
    files = {}
    for architecture in architectures:
        entry = lock['frankenphp']['architectures'][architecture]
        with ctx.activity('准备校验过的 PHP 原生运行时', target=architecture):
            binary = download(entry['url'], entry['sha256'],
                              ROOT / '.cache/native' / architecture / 'frankenphp',
                              settings['php'] if architecture == 'amd64' else None)
        binary.chmod(0o755)
        with binary.open('rb') as stream:
            header = stream.read(20)
        if header[:6] != b'\x7fELF\x02\x01' or int.from_bytes(header[18:20], 'little') != {'amd64': 62, 'arm64': 183}[architecture]:
            raise ValueError(f'runtime ELF architecture mismatch: {architecture}')
        files[architecture] = binary
    license_dir = owned_directory(ctx.work / 'runtime-licenses')
    for license in lock['licenses']:
        cached = download(license['url'], license['sha256'], ROOT / '.cache/native/licenses' / license['name'])
        shutil.copyfile(cached, license_dir / license['name'])
    ctx.metadata['native_runtime'] = lock
    return files, license_dir
