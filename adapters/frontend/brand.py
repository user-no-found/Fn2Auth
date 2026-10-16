"""Brand presentation assets without changing OTP format or upstream attribution."""
import json
from pathlib import Path


def apply_brand(app, components, metadata, icons):
    app.replace("resources/js/app.js", "    prefix: '2fauth_',",
                "    prefix: 'fn2auth_',\n    productName: branding.display_name,\n    productVersion: branding.version,")
    footer = "packages/ui/src/VueFooterContent/VueFooterContent.vue"
    components.replace(footer, "2FAuth – v{{ $2fauth.version }}",
                       "{{ $2fauth.productName }} – v{{ $2fauth.productVersion }}")
    components.replace(footer, "{{ user.email }}", "{{ user.name }}")
    components.replace(footer, "                    <!-- admin link -->\n                    <li", "                    <li class=\"column\"><router-link :to=\"{ name: 'backup' }\" class=\"is-link\">{{ $t('fn2auth.backup.title') }}</router-link></li>\n                    <!-- admin link -->\n                    <li")
    about = "resources/js/views/About.vue"
    app.replace(about, '<span class="is-size-5">2FAuth</span>',
                '<span class="is-size-5">{{ $2fauth.productName }}</span>')
    app.replace(about, "{{ $2fauth.version }}", "{{ $2fauth.productVersion }}")
    app.replace(about, '<img class="about-logo" src="logo.svg" alt="2FAuth logo" />',
                '<img class="about-logo" :src="$2fauth.config.subdirectory + \'/logo.png\'" :alt="$2fauth.productName" />')
    app.replace(about, 'router.options.history.state.back).name;',
                "router.options.history.state.back)?.name || 'accounts';")
    for size, target in [(64, "favicon.png"), (256, "favicon_lg.png"), (256, "logo.png")]:
        app.write("public/" + target, Path(icons["png"][size]).read_bytes())
    landing = "resources/views/landing.blade.php"
    if icons.get("ico"):
        app.write("public/favicon.ico", Path(icons["ico"]).read_bytes())
    else:
        app.replace(landing, '    <link rel="shortcut icon" href="{{ asset(\'favicon.ico\') }}" />\n', '')
    app.replace(landing, "initial-scale=1, maximum-scale=1, user-scalable=0, shrink-to-fit=no",
                "initial-scale=1, shrink-to-fit=no")
    prefix = metadata["gateway_prefix"]
    app.write("public/manifest.json", json.dumps({"name": metadata["display_name"],
        "short_name": metadata["display_name"], "start_url": prefix + "/", "scope": prefix + "/",
        "display": "standalone", "icons": [{"src": prefix + "/favicon_lg.png", "sizes": "256x256",
        "type": "image/png"}]}, indent=2) + "\n")
