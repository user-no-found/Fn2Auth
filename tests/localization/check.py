"""Exercise real localization and projection gates on disposable fixed-source copies."""
import argparse
import json
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
from unittest.mock import patch

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / 'scripts'))
from fn2auth_build import backend, localization
from fn2auth_build.inputs import write_json


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('source', type=Path)
    parser.add_argument('components', type=Path)
    parser.add_argument('resolved', type=Path)
    parser.add_argument('output', type=Path)
    parser.add_argument('--node', required=True)
    parser.add_argument('--compiler-source', type=Path)
    args = parser.parse_args()
    provenance = json.loads(args.resolved.read_text())
    review = json.loads((ROOT / 'locales/review.json').read_text())
    args.output.mkdir(parents=True, exist_ok=True)
    results = []
    with tempfile.TemporaryDirectory(prefix='locale-gates-', dir=args.output) as temporary:
        work = Path(temporary)
        pristine = work / 'pristine'
        for origin in ('upstream', 'components'):
            (pristine / origin).mkdir(parents=True)
        for key in review['source_inventory']:
            origin, relative = key.split('/', 1)
            source = args.source if origin == 'upstream' else args.components
            target = pristine / origin / relative
            target.parent.mkdir(parents=True, exist_ok=True)
            shutil.copyfile(source / relative, target)
        for name in ('en', 'zh-CN', 'zh-TW'):
            target = pristine / 'upstream/resources/lang' / (name + '.json')
            target.parent.mkdir(parents=True, exist_ok=True)
            shutil.copyfile(args.source / 'resources/lang' / target.name, target)

        def check(name, mutate=None, owned=False):
            case = work / name
            shutil.copytree(pristine, case)
            data = case / 'owned'
            shutil.copytree(ROOT / 'locales', data / 'locales')
            (data / 'frontend').mkdir()
            shutil.copyfile(ROOT / 'frontend/messages.en.json', data / 'frontend/messages.en.json')
            if mutate:
                mutate(case, data)
            try:
                with patch.object(localization, 'ROOT', data):
                    if owned:
                        localization.apply_owned(case / 'upstream', case / 'reports')
                    else:
                        localization.apply_and_check(case / 'upstream', case / 'components',
                                                     case / 'reports', provenance)
            except ValueError as error:
                if mutate is None:
                    raise
                results.append({'case': name, 'result': 'rejected', 'diagnostic': str(error)})
            else:
                if mutate is not None:
                    raise AssertionError('gate accepted invalid input: ' + name)
                results.append({'case': name, 'result': 'accepted'})

        def edit_json(path, edit):
            data = json.loads(path.read_text())
            edit(data)
            write_json(path, data)

        check('reviewed-input')
        check('changed-source', lambda case, data: edit_json(
            case / 'upstream/resources/lang/en.json',
            lambda catalog: catalog.update({'heading.session': 'Changed synthetic session'})))
        check('new-ui-source', lambda case, data: (
            case / 'upstream/resources/js/NewUnreviewed.vue').write_text('<template>New display text</template>'))
        check('missing-translation', lambda case, data: edit_json(
            data / 'locales/zh-CN.json', lambda catalog: catalog.pop('heading.session')))
        check('format-mismatch', lambda case, data: edit_json(
            data / 'locales/zh-CN.json', lambda catalog: catalog.update(
                {'validation.missing_with': 'Synthetic text missing the required placeholder'})))
        check('owned-reviewed-input', owned=True)
        check('missing-owned-key', lambda case, data: edit_json(
            data / 'locales/fn2auth.zh-CN.json', lambda catalog: catalog.pop(next(iter(catalog)))), owned=True)
        anchor = work / 'changed-anchor.php'
        anchor.write_text('<?php /* changed upstream entry */')
        try:
            backend.replace(anchor, "define('LARAVEL_START', microtime(true));", 'synthetic replacement')
        except ValueError as error:
            results.append({'case': 'changed-adapter-anchor', 'result': 'rejected', 'diagnostic': str(error)})
        else:
            raise AssertionError('changed adapter anchor was accepted')
        english, chinese = work / 'english.json', work / 'chinese.json'
        write_json(english, {'synthetic': '{broken'})
        write_json(chinese, {'synthetic': '{broken'})
        compiler = subprocess.run([args.node, str(ROOT / 'scripts/check-messages.mjs'),
                                   str(english), str(chinese), str(args.compiler_source or args.source),
                                   str(work / 'compiler.json')], capture_output=True, text=True)
        if compiler.returncode == 0:
            raise AssertionError('actual Vue compiler accepted malformed messages')
        report = json.loads((work / 'compiler.json').read_text())
        if not report['errors']:
            raise AssertionError('compiler negative control failed without an explained error')
        results.append({'case': 'malformed-vue-message', 'result': 'rejected'})
    write_json(args.output / 'checks.json', {'schema': 1, 'results': results})
    print(f'Localization and projection controls passed: {len(results)} cases.')


if __name__ == '__main__':
    main()
