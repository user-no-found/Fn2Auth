"""Verify packaged assets and real PHP through an isolated trusted Unix bridge."""
import json
import os
import subprocess
import time

from .inputs import ROOT, capped, native, write_json


def verify(ctx, fixture, settings, environment, modfile, dependencies):
    for name in ('playwright', 'chromium'):
        if name not in settings:
            raise ValueError('register --playwright and --chromium with ./setup.sh before package verification')
    reports = ctx.work / 'reports'
    env = {**environment, 'TMPDIR': str(fixture / 'tmp'),
           'PLAYWRIGHT_PACKAGE': settings['playwright'],
           'CHROMIUM_PATH': settings['chromium']}
    source = fixture / 'app/upstream/app'
    with ctx.activity('验证包内手机界面、扫码与取消边界'):
        native(ctx, [settings['node'], str(ROOT / 'tests/browser/run.mjs'),
                     str(source), str(reports / 'browser'), str(dependencies)], ROOT, env)
    bridge = ctx.work / 'bin/native-browser-bridge'
    native(ctx, [settings['go'], 'build', '-modfile=' + str(modfile),
                 '-o', str(bridge), './tests/native'], ROOT, env)
    stdout = reports / 'browser-bridge.jsonl'
    stderr = reports / 'browser-bridge.log'
    command = capped(['env', '-i', *(f'{key}={value}' for key, value in env.items()),
                      str(bridge), '-fixture', str(fixture)])
    with stdout.open('wb') as output, stderr.open('wb') as diagnostic:
        process = subprocess.Popen(command, cwd=ROOT, stdout=output, stderr=diagnostic)
    try:
        deadline = time.monotonic() + 30
        base = None
        while time.monotonic() < deadline:
            if process.poll() is not None:
                raise ValueError('isolated browser bridge exited; see browser-bridge.log')
            lines = stdout.read_text().splitlines()
            if lines:
                base = json.loads(lines[0])['url']
                break
            time.sleep(0.1)
        if base is None:
            raise ValueError('isolated browser bridge readiness timed out')
        with ctx.activity('验证包内真实 PHP 的扫码确认与账号操作'):
            native(ctx, [settings['node'], str(ROOT / 'tests/browser/native.mjs'), base,
                         str(reports / 'native-browser'),
                         str(reports / 'browser/public-fixture.png')], ROOT, env)
    finally:
        if process.poll() is None:
            process.terminate()
        try:
            process.wait(timeout=75)
        except subprocess.TimeoutExpired:
            process.kill()
            process.wait()
            raise ValueError('isolated browser bridge shutdown failed')
    summaries = {}
    for name in ('browser', 'native-browser'):
        report = json.loads((reports / name / 'report.json').read_text())
        results = report.get('results', [])
        unexpected = report.get('errors', report.get('diagnostics', {}).get('unexpected', []))
        if not results or any(not item.get('passed') for item in results) or unexpected:
            raise ValueError(f'{name} has failed checks or unexpected browser errors')
        summaries[name] = {'passed': len(results), 'unexpected_errors': len(unexpected)}
    write_json(reports / 'package-browser.json', {
        'schema': 1, 'results': summaries,
        'scope': 'actual FPK assets and PHP; current Go source; synthetic NAS identity and camera',
        'devices': 'no real fnOS gateway, mobile host, camera hardware or FN Connect'})
