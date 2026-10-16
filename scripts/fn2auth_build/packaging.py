"""Declare complete native payloads and their corresponding source bundle."""
import json
from pathlib import Path
import shutil
import tarfile

from fnpackkit.contracts import Lifecycle, LifecycleCommand, Payload, PayloadFile, UninstallPolicy
from .inputs import ROOT, digest, write_json
from .localization import text_hash


def installer_values():
    source = ROOT / 'packaging/installer.en.json'
    english = json.loads(source.read_text())
    translated = json.loads((ROOT / 'locales/installer.zh-CN.json').read_text())
    review = json.loads((ROOT / 'locales/installer.review.json').read_text())
    if review.get('source_file_sha256') != digest(source) or set(english) != set(translated):
        raise ValueError('installer translation source changed')
    for key, value in english.items():
        decision = review['decisions'][key]
        if decision['source_sha256'] != text_hash(value) or decision['action'] != 'translate' or not decision.get('reason'):
            raise ValueError('installer translation review incomplete')
    return translated


def source_bundle(work, upstream, components, runtime):
    target = work / 'corresponding-source.tar.gz'
    excluded = {'.git', '.cache', '.build', 'tasks', 'node_modules', 'vendor', 'dist', 'output', '__pycache__'}
    with tarfile.open(target, 'w:gz') as archive:
        for prefix, root in [('Fn2Auth', ROOT), ('2FAuth', upstream), ('2FAuth-Components', components), ('FnPackKit', runtime.parent)]:
            for path in sorted(root.rglob('*')):
                relative = path.relative_to(root)
                if any(part in excluded for part in relative.parts) or path.is_dir():
                    continue
                if path.name == '.env' or path.suffix == '.pyc':
                    continue
                if path.is_symlink() or not path.is_file():
                    raise ValueError(f'corresponding source requires regular files: {relative}')
                info = archive.gettarinfo(str(path), prefix + '/' + relative.as_posix())
                info.uid = info.gid = 0
                info.uname = info.gname = ''
                with path.open('rb') as stream:
                    archive.addfile(info, stream)
    return target


def lifecycle_declaration():
    required = ('TRIM_APPDEST', 'TRIM_PKGETC', 'TRIM_PKGVAR', 'TRIM_PKGTMP')
    phases = ('install_callback', 'upgrade_init', 'upgrade_callback',
              'uninstall_init', 'config_init', 'config_callback')
    hooks = {phase: LifecycleCommand('target', 'libexec/lifecycle', (phase,), required, run_as='package')
             for phase in phases}
    hooks.update({f'main:{action}': LifecycleCommand('target', 'libexec/lifecycle', (action,), required, run_as='package')
                  for action in ('start', 'stop', 'status')})
    return Lifecycle(hooks)


def uninstall_declaration():
    translations = installer_values()
    user = 'var/fn2auth/users/*/'
    protected = ('etc/fn2auth.json', *(user + path for path in (
        'database.sqlite', 'app.key', 'storage/oauth-private.key',
        'storage/oauth-public.key', 'snapshots')))
    transient = tuple(user + path for path in (
        'cache', 'tmp', 'storage/logs', 'storage/framework/cache',
        'storage/framework/views', 'storage/framework/sessions'))
    return UninstallPolicy(protected, transient,
                           translations['installer.uninstall_title'],
                           translations['installer.data_label'],
                           translations['installer.retain'], translations['installer.delete'])


def payload(ctx, architecture, source, binary, php, licenses, provenance, bundle):
    metadata = ctx.project.data
    files = [PayloadFile(binary, 'app/bin/fn2auth', 0o755),
             PayloadFile(php, 'app/runtime/frankenphp', 0o755),
             PayloadFile(bundle, 'app/upstream/app/public/source.tar.gz', 0o644),
             PayloadFile(ROOT / 'LICENSE', 'app/licenses/Fn2Auth.LICENSE', 0o644)]
    for name in ('app', 'bootstrap', 'config', 'database', 'lang', 'resources', 'public', 'vendor', 'routes'):
        root = source / name
        if not root.exists():
            continue
        for path in sorted(root.rglob('*')):
            if path.is_dir():
                continue
            relative = path.relative_to(source)
            if 'node_modules' in relative.parts or path.name in ('hot', '.gitignore'):
                continue
            if path.is_symlink():
                if relative.parts[:2] == ('vendor', 'bin'):
                    continue
                raise ValueError('unexpected runtime source symlink: ' + str(relative))
            files.append(PayloadFile(path, 'app/upstream/app/' + relative.as_posix(), 0o644))
    for name in ('artisan', 'composer.json', 'composer.lock', 'LICENSE'):
        files.append(PayloadFile(source / name, 'app/upstream/app/' + name, 0o644))
    for path in licenses.iterdir():
        files.append(PayloadFile(path, 'app/licenses/' + path.name, 0o644))
    generated = ctx.work / 'payload' / architecture
    template = generated / 'template'
    shutil.copytree(ROOT / 'packaging/fnos', template)
    record = {**provenance, 'architecture': architecture,
              'binary_sha256': {'fn2auth': digest(binary), 'frankenphp': digest(php)}}
    write_json(generated / 'build.json', record)
    files.append(PayloadFile(generated / 'build.json', 'app/build.json', 0o644))
    values = {'APP_ID': metadata['app_id'], 'DISPLAY_NAME': metadata['display_name'],
              'DESCRIPTION': metadata['description'], 'VERSION': metadata['version'],
              'PLATFORM': {'amd64': 'x86', 'arm64': 'arm'}[architecture],
              'GATEWAY_PREFIX': metadata['gateway_prefix'], 'GATEWAY_SOCKET': metadata['gateway_socket']}
    return Payload(template, files, values, f"{metadata['display_name']}_{metadata['version']}_{architecture}.fpk",
                   record, ['app/bin/fn2auth', 'app/runtime/frankenphp', 'app/build.json', 'app/upstream/app/vendor/autoload.php'], lifecycle=lifecycle_declaration(), uninstall=uninstall_declaration())
