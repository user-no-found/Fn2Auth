"""Install only the source lockfiles' native frontend and PHP dependency graphs."""
import json
import shutil

from .inputs import ROOT, digest, native, owned_directory, write_json


def install(ctx, upstream, components, settings, environment):
    lock = json.loads((ROOT / 'sources.lock.json').read_text())
    node, npm = settings['node'], settings['npm']
    environment = {**environment, 'npm_config_cache': str(owned_directory(ROOT / '.cache/npm')),
                   'COMPOSER_CACHE_DIR': str(owned_directory(ROOT / '.cache/composer')),
                   'COMPOSER_HOME': str(owned_directory(ctx.work / 'composer-home')),
                   'CI': 'true'}
    with ctx.activity('安装固定前端构建工具'):
        native(ctx, [npm, 'ci', '--ignore-scripts', '--no-audit', '--no-fund'], ROOT, environment)
    pnpm = ROOT / 'node_modules/pnpm/bin/pnpm.cjs'
    actual = native(ctx, [node, str(pnpm), '--version'], ROOT, environment, capture=True).strip()
    if actual != lock['pnpm_version']:
        raise ValueError('pnpm version does not match source lock')
    dependency_files = [upstream / 'composer.lock', upstream / 'package-lock.json', components / 'pnpm-lock.yaml']
    projection = ROOT / 'adapters/dependencies'
    selected = json.loads((projection / 'inputs.json').read_text())
    for name, expected in selected['base_files'].items():
        if digest(upstream / name) != expected:
            raise ValueError('frontend dependency base changed; review the pinned projection: ' + name)
    for name in ('package.json', 'package-lock.json'):
        shutil.copyfile(projection / name, upstream / name)
    before = {str(path): digest(path) for path in dependency_files}
    with ctx.activity('安装组件锁定依赖'):
        native(ctx, [node, str(pnpm), 'install', '--frozen-lockfile', '--store-dir',
                     str(owned_directory(ROOT / '.cache/pnpm'))], components, environment)
    with ctx.activity('安装应用锁定前端依赖'):
        native(ctx, [npm, 'ci', '--no-audit', '--no-fund'], upstream, environment)
    with ctx.activity('安装 PHP 生产依赖'):
        native(ctx, [settings['php'], 'php-cli', settings['composer'], 'install', '--no-dev',
                     '--no-scripts', '--prefer-dist', '--no-interaction', '--optimize-autoloader'], upstream, environment)
        native(ctx, [settings['php'], 'php-cli', settings['composer'], 'check-platform-reqs', '--no-dev'], upstream, environment)
    after = {str(path): digest(path) for path in dependency_files}
    if before != after:
        raise ValueError('dependency installation changed a source lockfile')
    report = {'source': ctx.metadata['sources'], 'pnpm_version': actual,
              'locks': {path.name: digest(path) for path in dependency_files}}
    write_json(ctx.work / 'reports/dependencies.json', report)
    return environment


def compile_frontend(ctx, upstream, components, settings, environment):
    pnpm = ROOT / 'node_modules/pnpm/bin/pnpm.cjs'
    for name in ('stores', 'formcontrols', 'ui'):
        with ctx.activity('构建上游组件', target=name):
            native(ctx, [settings['node'], str(pnpm), '--filter', '@2fauth/' + name, 'build'], components, environment)
    with ctx.activity('构建完整应用前端'):
        native(ctx, [settings['npm'], 'run', 'build'], upstream, environment)
