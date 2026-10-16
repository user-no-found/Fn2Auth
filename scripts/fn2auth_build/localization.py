"""Inventory every source message and apply explicitly reviewed Chinese overlays."""
import hashlib
import json
import re

from .inputs import ROOT, digest, write_json


def flatten(value, prefix=''):
    result = {}
    if isinstance(value, dict):
        for key, child in value.items():
            result.update(flatten(child, f'{prefix}.{key}' if prefix else key))
    elif isinstance(value, str):
        result[prefix] = value
    else:
        raise ValueError(f'locale leaf must be text: {prefix}')
    return result


def text_hash(value):
    return hashlib.sha256(value.encode()).hexdigest()


def tokens(value):
    # Vue-i18n named/list interpolation and Laravel :name substitutions.
    return sorted(re.findall(r'\{\s*[A-Za-z_0-9]+\s*\}|(?<![A-Za-z0-9]):[A-Za-z_][A-Za-z_0-9]*', value))


def inventory(upstream, components, reports, provenance):
    files = upstream / 'resources/lang'
    english = flatten(json.loads((files / 'en.json').read_text()))
    chinese = flatten(json.loads((files / 'zh-CN.json').read_text()))
    catalog = {'schema': 1, 'sources': provenance,
               'files': {name: digest(files / (name + '.json')) for name in ('en', 'zh-CN', 'zh-TW')},
               'messages': {key: {'source': value, 'source_sha256': text_hash(value),
                                  'zh_CN': chinese.get(key), 'tokens': tokens(value)}
                            for key, value in english.items()},
               'same_as_english': [key for key, value in english.items() if chinese.get(key) == value],
               'missing': sorted(english.keys() - chinese.keys()),
               'extra': sorted(chinese.keys() - english.keys())}
    catalog['source_inventory'] = {}
    for origin, tree, folders in [('upstream', upstream, ('resources/js', 'resources/views', 'app', 'lang')),
                                   ('components', components, ('packages',))]:
        for folder in folders:
            for path in sorted((tree / folder).rglob('*')):
                if path.is_file() and path.suffix in ('.vue', '.js', '.ts', '.php') and not any(part in ('node_modules', 'dist') for part in path.relative_to(tree).parts):
                    catalog['source_inventory'][origin + '/' + path.relative_to(tree).as_posix()] = digest(path)
    catalog['inventory_scope'] = 'JSON message keys plus hashes of UI/components/PHP source; reachability and newly hardcoded copy require review, not inferred automatically'
    write_json(reports / 'locale-source-catalog.json', catalog)
    return catalog


def apply_and_check(upstream, components, reports, provenance):
    catalog = inventory(upstream, components, reports, provenance)
    review = json.loads((ROOT / 'locales/review.json').read_text())
    overlay = flatten(json.loads((ROOT / 'locales/zh-CN.json').read_text()))
    if review.get('schema') != 1 or review.get('source_files') != catalog['files']:
        raise ValueError('locale source files changed; independent Chinese review required')
    if review.get('upstream_sha') != provenance['upstream']['revision'] or review.get('components_sha') != provenance['components']['revision']:
        raise ValueError('locale review does not match this build source')
    if review.get('source_inventory') != catalog['source_inventory']:
        raise ValueError('UI/component/PHP source inventory changed; review new display text')
    decisions = review.get('decisions', {})
    required = set(catalog['same_as_english']) | set(catalog['missing']) | set(overlay)
    if not required.issubset(decisions):
        raise ValueError('Chinese review missing decisions: ' + ', '.join(sorted(required - decisions.keys())))
    messages = json.loads((upstream / 'resources/lang/zh-CN.json').read_text())
    for key in required:
        source = catalog['messages'].get(key)
        decision = decisions[key]
        if not source or decision.get('source_sha256') != source['source_sha256'] or not decision.get('reason'):
            raise ValueError('locale review is stale/incomplete: ' + key)
        if key in overlay:
            if decision.get('action') != 'translate':
                raise ValueError('locale overlay needs translate decision: ' + key)
            # Upstream JSON keys contain literal dots; retain its flat format.
            messages[key] = overlay[key]
        elif decision.get('action') != 'retain':
            raise ValueError('locale decision lacks its overlay: ' + key)
    merged = flatten(messages)
    english = catalog['messages']
    if set(merged) != set(english):
        raise ValueError('Chinese catalog keys do not match English')
    for key, value in merged.items():
        if not value.strip() or tokens(value) != english[key]['tokens']:
            raise ValueError('Chinese placeholder/empty message mismatch: ' + key)
    write_json(upstream / 'resources/lang/zh-CN.json', messages)
    write_json(reports / 'locale-review.json', {'schema': 1, 'keys': len(merged),
               'review_sha256': digest(ROOT / 'locales/review.json'),
               'overlay_sha256': digest(ROOT / 'locales/zh-CN.json'),
               'traditional_chinese': 'upstream retained; not independently translated in this build'})


def check_compiler(ctx, upstream, settings, environment, reports):
    from .inputs import native
    native(ctx, [settings['node'], str(ROOT / 'scripts/check-messages.mjs'),
                 str(upstream / 'resources/lang/en.json'), str(upstream / 'resources/lang/zh-CN.json'),
                 str(upstream), str(reports / 'locale-compiler.json')], ROOT, environment)


def apply_owned(upstream, reports):
    english_file = ROOT / 'frontend/messages.en.json'
    english = flatten(json.loads(english_file.read_text()))
    review = json.loads((ROOT / 'locales/fn2auth.review.json').read_text())
    if review.get('schema') != 1 or review.get('source_file_sha256') != digest(english_file):
        raise ValueError('application message source changed; independent translation review required')
    for locale in ('zh-CN', 'zh-TW'):
        translated = json.loads((ROOT / 'locales' / ('fn2auth.' + locale + '.json')).read_text())
        if set(translated) != set(english):
            raise ValueError('application message keys differ: ' + locale)
        for key, message in english.items():
            decision = review['decisions'][key]
            if decision.get('source_sha256') != text_hash(message) or decision.get('action') != 'translate' or not decision.get('reason'):
                raise ValueError('application message review incomplete: ' + key)
            if not translated[key].strip() or tokens(translated[key]) != tokens(message):
                raise ValueError('application translation format mismatch: ' + key)
        target = upstream / 'resources/lang' / (locale + '.json')
        messages = json.loads(target.read_text())
        if set(translated) & set(messages):
            raise ValueError('application namespace collides with upstream')
        write_json(target, {**messages, **translated})
    write_json(reports / 'application-locale-review.json', {'schema': 1, 'keys': len(english),
               'source_sha256': digest(english_file), 'review_sha256': digest(ROOT / 'locales/fn2auth.review.json')})
