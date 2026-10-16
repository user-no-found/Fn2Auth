/** Follow available host presentation state without granting business authority. */
import { TrimApp } from '@trimjs/web-app'
import { watch } from 'vue'
import { waitFor } from './operation'
import branding from './branding.json'
import { localeName } from './locale'


function bindHost({ router, user, i18n }) {
    const host = new TrimApp({ debug: false })
    let disposed = false
    let reading = false
    const controller = new AbortController()
    const bounded = operation => waitFor(operation, { signal: controller.signal, timeout: 5000, code: 'HOST_TIMEOUT' })
    const apply = async () => {
        if (disposed || reading || document.hidden) return
        reading = true
        try {
            const config = await bounded(host.getPlatformConfig())
            if (disposed) return
            const root = document.documentElement
            if (user.preferences.theme === 'system') {
                root.classList.toggle('dark', config.theme === 'dark')
                root.dataset.theme = config.theme
                root.style.colorScheme = config.theme === 'dark' ? 'dark' : 'light'
            }
            if (user.preferences.lang === 'browser') i18n.global.locale.value = localeName(config.language, window.appLocales)
        } catch { /* Host display integration is optional; keep usable application settings. */ }
        finally { reading = false }
    }
    const title = () => {
        document.title = branding.display_name + ' · ' + i18n.global.t(router.currentRoute.value.name === 'backup' ? 'fn2auth.backup.title' : 'title.' + router.currentRoute.value.name)
        if (!disposed && !host.isStandaloneWeb) void bounded(host.setTitle(document.title)).catch(() => {})
    }
    const removeRoute = router.afterEach(() => queueMicrotask(title))
    const stopTitleWatch = watch(i18n.global.locale, title)
    const stopWatch = watch(() => [user.preferences.lang, user.preferences.theme], apply)
    const dispose = () => {
        disposed = true
        controller.abort()
        document.removeEventListener('visibilitychange', apply)
        removeRoute(); stopWatch(); stopTitleWatch()
        if (host.isWeb && !host.isStandaloneWeb) {
            void host.$off('os/theme', apply).catch(() => {})
            void host.$off('os/language', apply).catch(() => {})
        }
    }
    void (async () => {
        try {
            await bounded(host.ready())
            if (disposed || host.isStandaloneWeb) return
            await apply()
            title()
            document.addEventListener('visibilitychange', apply)
            if (host.isWeb && !host.isStandaloneWeb) {
                await bounded(host.$on('os/theme', apply))
                await bounded(host.$on('os/language', apply))
            }
        } catch { /* SDK availability cannot prevent using the vault. */ }
    })()
    return dispose
}

/** Reconnect presentation state when a mobile browser restores its cached page. */
export function connectHost(options) {
    let disconnect = bindHost(options)
    const hide = () => { disconnect?.(); disconnect = null }
    const show = event => { if (event.persisted && !disconnect) disconnect = bindHost(options) }
    window.addEventListener('pagehide', hide)
    window.addEventListener('pageshow', show)
    return () => {
        hide()
        window.removeEventListener('pagehide', hide)
        window.removeEventListener('pageshow', show)
    }
}
