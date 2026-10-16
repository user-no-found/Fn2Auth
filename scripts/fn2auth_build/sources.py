"""Resolve the fork once and pin its local component graph for one build."""
from datetime import datetime, timezone
import json
import os
from pathlib import Path

from buildflow.source import ResolvedSource, fetch_git, resolve_git
from .inputs import ROOT, digest, owned_directory, write_json


def prepare(ctx):
    metadata = ctx.project.data
    if metadata['upstream_policy'] != 'default-branch-head':
        raise ValueError('upstream_policy must be default-branch-head')
    url = f"https://github.com/{metadata['upstream']}.git"
    if metadata['upstream'] != 'user-no-found/Fnbak-2FAuth':
        raise ValueError('the approved business source is user-no-found/Fnbak-2FAuth')
    lock = json.loads((ROOT / 'sources.lock.json').read_text())
    owned_directory(ROOT / '.cache/git')
    saved = os.environ.get('FN2AUTH_RESOLVED_SOURCE')
    if saved:
        record = json.loads(Path(saved).read_text())
        if record['upstream']['url'] != url or not record.get('resolved_at'):
            raise ValueError('explicit source record does not match this project')
        source = ResolvedSource(**record['upstream'])
        if record.get('components') != lock['components']:
            raise ValueError('explicit source record component input changed')
    else:
        with ctx.activity('解析业务 fork 当前 HEAD', target=metadata['upstream']):
            source = resolve_git(url, runner=ctx.runner)
        record = {'resolved_at': datetime.now(timezone.utc).isoformat(),
                  'upstream': vars(source), 'components': lock['components']}
    work = owned_directory(ctx.work / 'sources')
    with ctx.activity('获取固定业务与组件源码', target=source.revision[:12]):
        upstream = fetch_git(source, work / '2FAuth', ROOT / '.cache/git', runner=ctx.runner)
        components = fetch_git(ResolvedSource(**lock['components']), work / '2FAuth-Components',
                               ROOT / '.cache/git', runner=ctx.runner)
    requirements = json.loads((upstream / 'package.json').read_text())['devDependencies']
    for name in ('formcontrols', 'stores', 'styles', 'ui'):
        expected = f'file:../2FAuth-Components/packages/{name}'
        manifest = json.loads((components / 'packages' / name / 'package.json').read_text())
        if requirements.get('@2fauth/' + name) != expected or manifest['name'] != '@2fauth/' + name:
            raise ValueError(f'component source contract changed: {name}')
    for name, tree in [('upstream', upstream), ('components', components)]:
        if digest(tree / 'LICENSE') != lock['licenses'][name]:
            raise ValueError(f'{name} license changed; review before building')
    record['dependency_locks'] = {name: digest(path) for name, path in {
        'composer': upstream / 'composer.lock', 'npm': upstream / 'package-lock.json',
        'pnpm': components / 'pnpm-lock.yaml'}.items()}
    write_json(ctx.work / 'resolved.json', record)
    ctx.metadata['sources'] = record
    return upstream, components, record
