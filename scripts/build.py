#!/usr/bin/env python3
"""Compose this application's native steps under FnPackKit and BuildFlow."""
import json
import os
from pathlib import Path
import shutil
import sys

from buildflow.contracts import Step
from buildflow.images import prepare_icons
from fn2auth_build import backend, browser, dependencies, environment, localization, packaging, runtime, sources, verification
from fn2auth_build.inputs import ROOT, digest, native, owned_directory, project_digest, write_json

sys.path.insert(0, str(ROOT))
try:
    from adapters.frontend.apply import apply_frontend
finally:
    sys.path.remove(str(ROOT))


def plan(project):
    metadata = project.data
    if metadata['app_id'] != 'fn2auth' or metadata['gateway_prefix'] != '/app/fn2auth' or metadata['gateway_socket'] != 'app.sock':
        raise ValueError('application identity differs from the approved fn2auth deployment contract')
    architectures = metadata['build']['architectures']
    if not architectures or len(set(architectures)) != len(architectures) or set(architectures) - {'amd64', 'arm64'}:
        raise ValueError('build.architectures must contain unique amd64/arm64 targets')
    state = {}

    def prepare(ctx):
        if os.geteuid() == 0:
            raise ValueError('normal application build must use a nonroot account')
        state['input_digest'] = project_digest()
        state['reports'] = owned_directory(ctx.work / 'reports')
        state['source'], state['components'], state['resolved'] = sources.prepare(ctx)
        state['settings'], state['tools'] = environment.load()
        state['environment'] = environment.command_environment(state['settings'], ctx.work)
        state['environment']['ASSET_URL'] = metadata['gateway_prefix']
        toolkit = Path(os.environ.get('FNPACKKIT_PATH', ''))
        if not toolkit.is_absolute() or not (toolkit / 'runtime/go.mod').is_file():
            raise ValueError('FNPACKKIT_PATH must explicitly select the shared runtime checkout')
        state['runtime'] = toolkit / 'runtime'

    def install(ctx):
        state['environment'] = dependencies.install(ctx, state['source'], state['components'], state['settings'], state['environment'])
        state['php'], state['licenses'] = runtime.prepare(ctx, state['settings'], architectures)

    def project_source(ctx):
        source, components, reports = state['source'], state['components'], state['reports']
        with ctx.activity('验证中文与适配门禁负控制'):
            native(ctx, [sys.executable, str(ROOT / 'tests/localization/check.py'), str(source),
                         str(components), str(ctx.work / 'resolved.json'),
                         str(reports / 'localization-controls'), '--node', state['settings']['node']],
                   ROOT, state['environment'])
        localization.apply_and_check(source, components, reports, state['resolved'])
        icon = metadata['branding']['icon']
        with ctx.activity('生成应用图标与浏览器资源'):
            icons = prepare_icons(ROOT / icon['source'], ctx.work / 'web-icons', png_sizes=(64, 256),
                                  variants={int(key): ROOT / path for key, path in icon['variants'].items()}, ico_sizes=(16, 32, 64))
        backend.apply(source, reports)
        apply_frontend(source, components, metadata, reports, icons)
        localization.apply_owned(source, reports)
        localization.check_compiler(ctx, source, state['settings'], state['environment'], reports)
        state['icons'] = icons['metadata']

    def compile_all(ctx):
        settings, env = state['settings'], state['environment']
        dependencies.compile_frontend(ctx, state['source'], state['components'], settings, env)
        modfile = ctx.work / 'adapter.mod'
        shutil.copyfile(ROOT / 'go.mod', modfile)
        shutil.copyfile(ROOT / 'go.sum', modfile.with_suffix('.sum'))
        native(ctx, [settings['go'], 'mod', 'edit', '-modfile=' + str(modfile),
                     '-replace=github.com/user-no-found/FnPackKit/runtime=' + str(state['runtime'])], ROOT, env)
        state['modfile'] = modfile
        state['binaries'] = {}
        for architecture in architectures:
            target = owned_directory(ctx.work / 'bin' / architecture) / 'fn2auth'
            flags = f"-s -w -X main.version={metadata['version']} -X main.displayName={metadata['display_name']} -X main.buildID={state['resolved']['upstream']['revision']}"
            with ctx.activity('编译原生应用入口', target=architecture):
                native(ctx, [settings['go'], 'build', '-modfile=' + str(modfile), '-trimpath', '-ldflags', flags,
                             '-o', str(target), './cmd/fn2auth'], ROOT, {**env, 'GOOS': 'linux', 'GOARCH': architecture})
            state['binaries'][architecture] = target
        unchanged()
        state['provenance'] = {'product': metadata, 'sources': state['resolved'], 'toolchain': state['tools'],
                               'project_sha256': state['input_digest'], 'icons': state['icons'],
                               'native_runtime': ctx.metadata['native_runtime']}
        write_json(state['reports'] / 'build-inputs.json', state['provenance'])
        state['bundle'] = packaging.source_bundle(ctx.work, state['source'], state['components'], state['runtime'])

    def unchanged():
        if project_digest() != state['input_digest']:
            raise ValueError('maintained application inputs changed during this build; rebuild before publishing')

    def payloads(ctx):
        unchanged()
        return [packaging.payload(ctx, architecture, state['source'], state['binaries'][architecture],
                                  state['php'][architecture], state['licenses'], state['provenance'], state['bundle'])
                for architecture in architectures]

    def verify(ctx, artifacts):
        fixture = verification.verify(ctx, artifacts, state['settings'], state['environment'], state['modfile'])
        browser.verify(ctx, fixture, state['settings'], state['environment'], state['modfile'], state['source'])
        unchanged()

    return {'steps': [Step('固定业务源码与构建输入', prepare), Step('原生依赖与 PHP 运行时', install),
                      Step('独立中文审查与应用投影', project_source), Step('原生前端与双架构入口', compile_all)],
            'payload': payloads, 'verify': verify}
