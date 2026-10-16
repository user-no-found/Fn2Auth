/** Serve actual production assets with explicit synthetic, public API fixtures. */
import http from 'node:http'
import { readFile } from 'node:fs/promises'
import path from 'node:path'

export const prefix = '/app/fn2auth'
export const preferences = { theme: 'system', lang: 'en', getOtpOnRequest: true, kickUserAfter: 0,
    AutoSaveQrcodedAccount: true, showEmailInFooter: true, activeGroup: 0, defaultGroup: 0,
    displayMode: 'list', showAccountsIcons: false, sortOrder: 'free', showOtpAsDot: false,
    showNextOtp: false, autoCloseTimeout: 0, iconSource: 'logolib', iconCollection: 'selfh',
    iconVariant: 'regular', showQuickNavMenus: true, useGroupChips: true, showVirtualChips: true }
export const account = { id: 1, service: 'Public fixture', account: 'demo@example.invalid',
    otp_type: 'totp', secret: 'JBSWY3DPEHPK3PXP', algorithm: 'sha1', digits: 6, period: 30,
    counter: 0, group_id: null, icon: null, is_shared: false, is_borrowed: false }

export async function fixture(source) {
    const manifest = JSON.parse(await readFile(path.join(source, 'public/build/manifest.json')))
    const entry = manifest['resources/js/app.js']
    const globals = { appSettings: { disableRegistration: true, enableSharing: false, checkForUpdate: false },
        appConfig: { subdirectory: prefix, proxyAuth: true, proxyLogoutUrl: null }, urls: {},
        defaultPreferences: preferences, lockedPreferences: ['AutoSaveQrcodedAccount'],
        appVersion: '8.0.2', isDemoApp: false, isTestingApp: true, appLocales: ['en', 'zh-CN', 'zh-TW'] }
    const html = `<!doctype html><html lang="en"><head><meta name="viewport" content="width=device-width, initial-scale=1">
        ${entry.css.map(file => `<link rel="stylesheet" href="${prefix}/build/${file}">`).join('')}</head>
        <body><div id="app"></div><script>${Object.entries(globals).map(([key,value]) => `window.${key}=${JSON.stringify(value)};`).join('')}</script>
        <script type="module" src="${prefix}/build/${entry.file}"></script></body></html>`
    const requests = []
    const state = { language: 'en', configStatus: 200, previewStatus: 200, previewDelay: 0, otpStatus: 200, writeStatus: 201, backupCode: null, accounts: [] }
    const server = http.createServer(async (req, res) => {
        const url = new URL(req.url, 'http://localhost')
        const target = url.pathname
        const body = []
        for await (const chunk of req) body.push(chunk)
        requests.push({ method: req.method, path: target, marker: req.headers['x-fn2auth-origin'], body: Buffer.concat(body).toString() })
        const json = (data, status = 200) => { res.writeHead(status, { 'Content-Type': 'application/json', 'Cache-Control': 'no-store' }); res.end(JSON.stringify(data)) }
        if (target === prefix + '/_fn2auth/config') return json({ schema: 1, display_name: 'Fn2Auth', version: '0.0.1', gateway_prefix: prefix, user: { username: 'trusted-nas-user' } }, state.configStatus)
        if (target === prefix + '/api/v1/user') return json({ id: 7, name: 'local-package-user', email: 'local@package.invalid', preferences: { ...preferences, lang: state.language }, is_admin: false, appSettings: globals.appSettings })
        if (target === prefix + '/_fn2auth/backup/export') { res.writeHead(200, { 'Content-Type': 'application/octet-stream', 'Content-Disposition': 'attachment; filename=fixture.age' }); res.end('public fixture, encryption tested in backend suite'); return }
        if (target === prefix + '/_fn2auth/backup/preview') return json({ schema: 1, preview_token: 'fixture-token', expires_in: 300, accounts: [{ service: 'Public HOTP', account: 'public@example.invalid', otp_type: 'hotp', exists: true, counter: 42 }] })
        if (target === prefix + '/_fn2auth/backup/import') return appBackup()
        function appBackup() { return state.backupCode ? json({ schema: 1, status: 'rejected', code: state.backupCode }, 409) : json({ schema: 1, imported: 1, skipped: 0, updated_counters: 1 }) }
        if (target === prefix + '/refresh-csrf') return json({})
        if (target.endsWith('/twofaccounts/preview')) {
            await new Promise(resolve => setTimeout(resolve, state.previewDelay))
            return json(state.previewStatus === 200 ? account : { message: 'Fixture rejected', errors: { uri: ['Invalid URI'] } }, state.previewStatus)
        }
        if (target.endsWith('/twofaccounts/otp') || target.endsWith('/1/otp')) return json({ password: '123456', generated_at: Date.now() / 1000 }, state.otpStatus)
        if (target === prefix + '/api/v1/twofaccounts' && req.method === 'POST') return json(account, state.writeStatus)
        if (target === prefix + '/api/v1/twofaccounts') return json(state.accounts)
        if (target === prefix + '/api/v1/groups') return json([{ id: 0, name: 'All' }])
        if (target === prefix + '/api/v1/icons/packs') return json([])
        if (target.startsWith(prefix + '/api/') || target.startsWith(prefix + '/user/')) return json({ message: 'Unimplemented fixture route' }, 404)
        if (target === '/frame') { res.end(`<iframe src="${prefix}/capture" allow="camera" style="width:100%;height:850px"></iframe>`); return }
        if (target.startsWith(prefix + '/build/') || target.endsWith('.png')) {
            try {
                const file = path.resolve(source, 'public', target.slice(prefix.length + 1))
                if (!file.startsWith(path.resolve(source, 'public') + path.sep)) throw new Error('path')
                const type = { '.js': 'text/javascript', '.css': 'text/css', '.wasm': 'application/wasm', '.png': 'image/png' }[path.extname(file)]
                const bytes = await readFile(file)
                res.writeHead(200, { 'Content-Type': type || 'application/octet-stream' }); res.end(bytes); return
            } catch { res.writeHead(404); res.end(); return }
        }
        res.writeHead(200, { 'Content-Type': 'text/html' }); res.end(html)
    })
    await new Promise(resolve => server.listen(0, '127.0.0.1', resolve))
    return { state, requests, url: `http://127.0.0.1:${server.address().port}`, close: () => new Promise(resolve => server.close(resolve)) }
}
