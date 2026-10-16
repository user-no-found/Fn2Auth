"""Integrate owned modules while retaining the complete pinned Vue application."""
import json
from pathlib import Path

from .brand import apply_brand
from .projection import Projection


def apply_frontend(source, components, metadata, reports, icons):
    """Project frontend and produce per-file digests for the main build report."""
    app, ui = Projection(source), Projection(components)
    owned = Path(__file__).resolve().parents[2] / "frontend"
    for path in sorted(owned.iterdir()):
        if path.suffix in {".js", ".vue"}:
            app.write("resources/js/fn2auth/" + path.name, path.read_bytes())
    brand = {key: metadata[key] for key in ("display_name", "version", "gateway_prefix")}
    app.write("resources/js/fn2auth/branding.json", json.dumps(brand, indent=2) + "\n")
    english = json.loads(app.read("resources/lang/en.json"))
    if any(key == "fn2auth" or key.startswith("fn2auth.") for key in english):
        raise ValueError("upstream already defines reserved fn2auth locale namespace")
    english.update(flatten_messages(json.loads((owned / "messages.en.json").read_text())))
    app.write("resources/lang/en.json", json.dumps(english, ensure_ascii=False, indent=4) + "\n")
    app.write("resources/js/views/Start.vue", '<script>\nexport { default } from "@/fn2auth/Start.vue"\n</script>\n')
    app.write("resources/js/views/twofaccounts/Capture.vue", '<script>\nexport { default } from "@/fn2auth/Capture.vue"\n</script>\n')
    app.write("resources/js/services/httpClientFactory.js", "export { httpClientFactory } from '@/fn2auth/http-client'\n")
    app.write("resources/js/router/middlewares/authGuard.js", "export { default } from '@/fn2auth/auth-guard'\n")
    app.write("resources/js/router/middlewarePipeline.js", "export { default } from '@/fn2auth/middleware-pipeline'\n")
    app.replace("resources/js/router/index.js", "    middlewares[0]({\n        ...context,\n        nextMiddleware: middlewarePipeline(context, middlewares, 1),\n    });",
                "    return middlewarePipeline(context, middlewares, 0)();")
    app.replace("resources/js/app.js", "import router from './router'",
                "import router from './router'\nimport branding from './fn2auth/branding.json'\nimport { connectHost } from './fn2auth/host'")
    app.replace("resources/js/app.js", "app.mount('#app')", "app.mount('#app')\nconnectHost({ router, user, i18n })")
    app.replace("resources/js/App.vue", "    import { Kicker } from '@2fauth/ui'",
                "    import { Kicker } from '@2fauth/ui'\n    import SessionNotice from './fn2auth/SessionNotice.vue'")
    app.replace("resources/js/App.vue", '<main class="main-section">', '<SessionNotice />\n    <main class="main-section">')
    app.replace("resources/js/router/index.js", "\troutes: [", "\troutes: [\n        { path: '/backup', name: 'backup', component: () => import('../fn2auth/Backup.vue'), meta: { middlewares: [authGuard], watchedByKicker: true } },")
    apply_preferences(app)
    apply_confirmed_creation(app)
    app.replace("vite.config.js", "const ASSET_URL = process.env.ASSET_URL || ''", "")
    app.replace("vite.config.js", "    base: `${ASSET_URL}`,", "    base: " + json.dumps(brand["gateway_prefix"] + "/build/") + ",")
    app.replace("vite.config.js", "    build: {", "    worker: { format: 'es' },\n    build: {")
    csp = "app/Http/Middleware/AddContentSecurityPolicyHeaders.php"
    anchor = "$directives['object-src']      = \"object-src 'none'\";"
    app.replace(csp, anchor, anchor + "\n        $directives['worker-src'] = \"worker-src 'self'\";\n        $directives['media-src'] = \"media-src 'self' blob:\";")
    apply_brand(app, ui, brand, icons)
    result = {"schema": 1, "source": app.files, "components": ui.files, "branding": brand}
    destination = Path(reports) / "frontend.json"
    destination.parent.mkdir(parents=True, exist_ok=True)
    destination.write_text(json.dumps(result, indent=2) + "\n")
    return result


def apply_preferences(app):
    path = "resources/js/stores/user.js"
    value = app.read(path)
    start = value.index("        applyLanguage() {")
    end = value.index("\n        },", start) + len("\n        },")
    app.replace(path, value[start:end], """        applyLanguage() {
            const language = this.preferences.lang === 'browser' ? navigator.language : this.preferences.lang
            this.$i18n.global.locale = localeName(language, window.appLocales)
        },""")
    app.write(path, "import { localeName } from '@/fn2auth/locale'\n" + app.read(path))
    app.replace(path, "            groups.fetch()", "            await groups.fetch()")
    config = "config/2fauth.php"
    app.replace(config, "'AutoSaveQrcodedAccount'    => envUnlessEmpty('USERPREF_DEFAULT__AUTO_SAVE_QRCODED_ACCOUNT', false)",
                "'AutoSaveQrcodedAccount'    => false")
    app.replace(config, "Helpers::lockedPreferences(Arr::except($preferences, $nonLockablePreferences))",
                "array_values(array_unique(array_merge(Helpers::lockedPreferences(Arr::except($preferences, $nonLockablePreferences)), ['AutoSaveQrcodedAccount'])))")


def apply_confirmed_creation(app):
    path = "resources/js/views/twofaccounts/CreateUpdate.vue"
    value = app.read(path)
    start = value.index("            if (user.preferences.AutoSaveQrcodedAccount) {")
    end = value.index("                // We prefill and show the quick form", start)
    app.replace(path, value[start:end], "            {\n")
    value = app.read(path)
    app.replace(path, "error.response.data.errors.uri", "error.response?.data?.errors?.uri",
                count=value.count("error.response.data.errors.uri"))
    value = app.read(path)
    start = value.index("                    <!-- qcode fileupload -->")
    end = value.index("                    <!-- service -->", start)
    app.replace(path, value[start:end], "                    <RouterLink v-if=\"!isEditMode\" class=\"button mb-4\" :to=\"{ name: 'capture' }\">{{ $t('label.prefill_using_qrcode') }}</RouterLink>\n")
    value = app.read(path)
    start = value.index("    /**\n     * Sends a QR code to backend")
    end = value.index("    /**", start + 7)
    app.replace(path, value[start:end], "")
    app.replace(path, "    const qrcodeForm = reactive(new Form({\n        qrcode: null\n    }))\n", "")
    app.replace(path, "    const qrcodeInputLabel = ref(null)\n    const qrcodeInput = ref(null)\n", "")
    form = "resources/js/components/formElements/Form.js"
    app.replace(form, "                    if (error.response?.status != 422) {\n                        reject(error)\n                    }",
                "                    reject(error)")


def flatten_messages(messages, prefix=""):
    result = {}
    for key, value in messages.items():
        name = prefix + key
        if isinstance(value, dict):
            result.update(flatten_messages(value, name + "."))
        else:
            result[name] = value
    return result
