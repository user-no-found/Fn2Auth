"""Project the private PHP instance boundary into an exact upstream checkout."""
import shutil

from .inputs import ROOT, digest, write_json


def replace(path, before, after):
    text = path.read_text()
    if text.count(before) != 1:
        raise ValueError(f'upstream adapter anchor changed: {path.name}')
    path.write_text(text.replace(before, after))


def apply(upstream, reports):
    replacements = {}
    for name in ('request', 'bootstrap', 'backup', 'snapshot'):
        target = upstream / 'bootstrap' / ('fn2auth-' + name + '.php')
        if target.exists():
            raise ValueError('upstream unexpectedly contains integration bootstrap')
        shutil.copyfile(ROOT / 'adapters/backend' / (name + '.php'), target)
        replacements[str(target.relative_to(upstream))] = digest(target)
    replace(upstream / 'public/index.php', "define('LARAVEL_START', microtime(true));",
            "require __DIR__.'/../bootstrap/fn2auth-request.php';\n\ndefine('LARAVEL_START', microtime(true));")
    replace(upstream / 'bootstrap/app.php', 'return $app;', "require __DIR__.'/fn2auth-bootstrap.php';\n\nreturn $app;")
    replace(upstream / 'app/Http/Middleware/AddContentSecurityPolicyHeaders.php',
            '"frame-ancestors \'none\'"', '"frame-ancestors \'self\'"')
    # Prevent upstream self-registration or creation of additional vault identities.
    replace(upstream / 'app/Extensions/RemoteUserProvider.php',
            "$name  = substr($identifier['id'], 0, 191);", "$name = getenv('FN2AUTH_USERNAME');\n        if (!$name || $identifier['id'] !== $name) { throw new Exception('instance_identity_mismatch'); }")
    replace(upstream / 'app/Extensions/RemoteUserProvider.php',
            "            if (User::count() === 1) {\n                $user->promoteToAdministrator();\n                $user->save();\n            }", "")
    replace(upstream / 'config/2fauth.php', "'useEncryption' => false,", "'useEncryption' => true,")
    replace(upstream / 'config/2fauth.php', "'checkForUpdate' => true,", "'checkForUpdate' => false,")
    replace(upstream / 'config/2fauth.php', "'disableRegistration' => false,", "'disableRegistration' => true,")
    for name in ('public/index.php', 'bootstrap/app.php', 'app/Extensions/RemoteUserProvider.php',
                 'app/Http/Middleware/AddContentSecurityPolicyHeaders.php'):
        replacements[name] = digest(upstream / name)
    write_json(reports / 'backend-projection.json', replacements)
