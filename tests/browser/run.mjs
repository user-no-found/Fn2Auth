/** Browser acceptance checks against a complete built app, with simulated APIs. */
import assert from 'node:assert/strict'
import { createRequire } from 'node:module'
import { pathToFileURL } from 'node:url'
import { readFile, mkdir, writeFile } from 'node:fs/promises'
import path from 'node:path'
import { createHash } from 'node:crypto'
import { fixture, prefix } from './fixture.mjs'
import { cameraChecks } from './camera.mjs'
import { backupChecks } from './backup.mjs'
import { hostChecks } from './host.mjs'

const source = path.resolve(process.argv[2] || '.build/frontend-check/2FAuth')
const output = path.resolve(process.argv[3] || '.build/frontend-check/browser')
if (!process.env.PLAYWRIGHT_PACKAGE || !process.env.CHROMIUM_PATH) throw new Error('Set PLAYWRIGHT_PACKAGE to an installed Playwright package.json and CHROMIUM_PATH to an installed Chromium executable')
const require = createRequire(path.resolve(process.env.PLAYWRIGHT_PACKAGE))
const { chromium } = require('playwright')
const writerSource = path.resolve(process.argv[4] || source)
const writer = await import(pathToFileURL(path.join(writerSource, 'node_modules/zxing-wasm/dist/es/writer/index.js')))
writer.setZXingModuleOverrides({ wasmBinary: await readFile(path.join(writerSource, 'node_modules/zxing-wasm/dist/writer/zxing_writer.wasm')) })
const uri = 'otpauth://totp/Public%20fixture:demo@example.invalid?secret=JBSWY3DPEHPK3PXP&issuer=Public%20fixture'
const qr = await writer.writeBarcodeToImageFile(uri, { width: 500, height: 500 })
assert.equal(qr.error, '')
const png = Buffer.from(await qr.image.arrayBuffer())
await mkdir(output, { recursive: true })
await writeFile(path.join(output, 'public-fixture.png'), png)
const app = await fixture(source)
const browser = await chromium.launch({ executablePath: process.env.CHROMIUM_PATH, headless: true, args: ['--no-sandbox'] })
const results = []
const errors = []
const page = await browser.newPage({ viewport: { width: 390, height: 844 } })
page.setDefaultTimeout(10000)
page.on('pageerror', error => { errors.push(error.message); console.error('PAGE', error.message) })
page.on('console', message => { if (message.type() === 'error') errors.push(message.text()) })
async function check(name, run) {
    try { await run(); results.push({ name, passed: true }); console.log('PASS', name) }
    catch (error) { results.push({ name, passed: false, error: error.stack }); console.error('FAIL', name, error.message) }
}
const open = async (route = '/capture') => { await page.goto(app.url + prefix + route); await page.getByTestId('qr-image-input').waitFor() }
const upload = () => page.getByTestId('qr-image-input').setInputFiles({ name: 'public.png', mimeType: 'image/png', buffer: png })
try {
    await check('Three independent entries, trusted NAS identity and mobile layout', async () => {
        await open()
        assert.equal(await page.getByTestId('qr-photo-input').getAttribute('capture'), 'environment')
        assert.equal(await page.getByTestId('qr-image-input').getAttribute('capture'), null)
        assert.ok(await page.getByTestId('camera-start').isVisible())
        assert.ok((await page.locator('#btnEmailMenu').textContent()).includes('trusted-nas-user'))
        assert.ok(!(await page.locator('body').textContent()).includes('local@package.invalid'))
        assert.ok((await page.title()).startsWith('Fn2Auth'))
        assert.ok(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth))
        await page.screenshot({ path: path.join(output, 'mobile-camera.png'), fullPage: true })
    })
    await check('Local WASM image decoding reaches confirmation without account creation', async () => {
        await upload()
        await page.waitForURL('**/account/create')
        await page.locator('.modal-otp.is-active').waitFor()
        assert.equal(app.requests.filter(r => r.method === 'POST' && r.path === prefix + '/api/v1/twofaccounts').length, 0)
        assert.ok(app.requests.some(r => r.path.endsWith('.wasm')))
        assert.ok(app.requests.filter(r => r.path.includes('/api/')).every(r => r.marker === 'same-origin'))
        await page.screenshot({ path: path.join(output, 'scan-confirmation.png'), fullPage: true })
    })
    await check('Corrupt and oversize pictures settle and permit retry', async () => {
        await open()
        await page.getByTestId('qr-image-input').setInputFiles({ name: 'bad.png', mimeType: 'image/png', buffer: Buffer.from('invalid') })
        await page.getByTestId('qr-error').waitFor()
        assert.ok(await page.getByTestId('qr-image-input').isEnabled())
        await page.getByTestId('qr-image-input').setInputFiles({ name: 'large.png', mimeType: 'image/png', buffer: Buffer.alloc(20 * 1024 * 1024 + 1) })
        await page.waitForFunction(() => document.querySelector('[data-testid="qr-error"]')?.textContent.includes('too large'))
    })
    await check('Huge declared raster dimensions are rejected before attempting bitmap decode', async () => {
        await open()
        const header = Buffer.alloc(24)
        Buffer.from([137, 80, 78, 71, 13, 10, 26, 10]).copy(header)
        header.write('IHDR', 12); header.writeUInt32BE(100000, 16); header.writeUInt32BE(100000, 20)
        await page.getByTestId('qr-image-input').setInputFiles({ name: 'dimensions.png', mimeType: 'image/png', buffer: header })
        await page.getByTestId('qr-error').waitFor()
        assert.ok((await page.getByTestId('qr-error').textContent()).includes('too large'))
    })
    await check('419 refreshes CSRF once, never replays preview, and permits explicit retry', async () => {
        await open(); app.state.previewStatus = 419
        const start = app.requests.length
        await upload(); await page.getByTestId('qr-error').waitFor()
        const fresh = app.requests.slice(start)
        assert.equal(fresh.filter(r => r.path.endsWith('/twofaccounts/preview')).length, 1)
        assert.equal(fresh.filter(r => r.path.endsWith('/refresh-csrf')).length, 1)
        assert.ok(await page.getByTestId('qr-image-input').isEnabled())
        app.state.previewStatus = 200
    })
    await check('Cancel pending server preview prevents late navigation', async () => {
        await open(); app.state.previewDelay = 1200
        const preview = page.waitForRequest(request => request.url().endsWith('/twofaccounts/preview'))
        await upload(); await preview
        await page.getByTestId('qr-cancel').click()
        await page.waitForTimeout(1400)
        assert.ok(page.url().endsWith('/capture'))
        assert.ok(await page.getByTestId('qr-image-input').isEnabled())
        app.state.previewDelay = 0
    })
    await check('Rejected NAS session shows a retry notice and no local-login loop', async () => {
        app.state.configStatus = 401
        await page.goto(app.url + prefix + '/capture')
        await page.getByTestId('session-error').waitFor()
        assert.ok(!page.url().includes('/login'))
        app.state.configStatus = 200
        await page.getByTestId('session-reload').click()
        await page.getByTestId('camera-start').waitFor()
    })
    await check('Camera denial remains recoverable through file and manual entries', async () => {
        await page.addInitScript(() => Object.defineProperty(navigator.mediaDevices, 'getUserMedia', { value: () => Promise.reject(new DOMException('denied', 'NotAllowedError')) }))
        await open(); await page.getByTestId('camera-start').click()
        await page.getByTestId('camera-error').waitFor()
        assert.ok(await page.getByTestId('qr-manual').isVisible())
        assert.ok(await page.getByTestId('qr-photo-input').isEnabled())
    })
    await cameraChecks(browser, app, check)
    await backupChecks(page, app, check)
    await hostChecks(browser, app, check)
    await check('Simplified Chinese user preference renders all camera and backup labels at mobile width', async () => {
        app.state.language = 'zh-CN'
        await open()
        const chinese = JSON.parse(await readFile(new URL('../../locales/fn2auth.zh-CN.json', import.meta.url)))
        assert.equal((await page.getByTestId('camera-start').textContent()).trim(), chinese['fn2auth.camera.start'])
        assert.ok(!(await page.locator('body').textContent()).includes('fn2auth.'))
        await page.screenshot({ path: path.join(output, 'mobile-camera-zh-CN.png'), fullPage: true })
        await page.goto(app.url + prefix + '/backup')
        await page.getByTestId('backup-page').waitFor()
        assert.ok(!(await page.getByTestId('backup-page').textContent()).includes('fn2auth.'))
        assert.ok(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth))
        await page.screenshot({ path: path.join(output, 'mobile-backup-zh-CN.png'), fullPage: true })
        app.state.language = 'en'
    })
} finally {
    const assetHash = createHash('sha256').update(await readFile(path.join(source, 'public/build/manifest.json'))).digest('hex')
    await writeFile(path.join(output, 'report.json'), JSON.stringify({ schema: 1, boundary: 'Built Vue assets, synthetic API and desktop Chromium; not real fnOS/phones', assetHash, results, diagnostics: { expectedInjectedHTTP: errors.filter(error => /status of (401|419|409)/.test(error)), unexpected: errors.filter(error => !/status of (401|419|409)/.test(error)) }, requests: app.requests }, null, 2))
    await browser.close(); await app.close()
}
if (results.some(result => !result.passed)) process.exitCode = 1
