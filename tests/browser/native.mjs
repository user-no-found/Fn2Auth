/** Exercise built Vue through the isolated Go/Unix/PHP bridge and a fresh vault. */
import assert from 'node:assert/strict'
import { createRequire } from 'node:module'
import { readFile, mkdir, writeFile } from 'node:fs/promises'
import path from 'node:path'

const [base, outputArg, imageArg] = process.argv.slice(2)
if (!base || !outputArg || !imageArg || !process.env.PLAYWRIGHT_PACKAGE || !process.env.CHROMIUM_PATH) {
    throw new Error('Provide bridge base URL, evidence directory, public QR PNG, PLAYWRIGHT_PACKAGE and CHROMIUM_PATH')
}
const url = new URL(base)
assert.equal(url.hostname, '127.0.0.1', 'Only an isolated local fixture is supported')
const output = path.resolve(outputArg)
await mkdir(output, { recursive: true })
const require = createRequire(path.resolve(process.env.PLAYWRIGHT_PACKAGE))
const { chromium } = require('playwright')
const browser = await chromium.launch({ executablePath: process.env.CHROMIUM_PATH, headless: true, args: ['--no-sandbox'] })
const page = await browser.newPage({ viewport: { width: 390, height: 844 } })
page.setDefaultTimeout(30000)
const errors = [], results = [], calls = []
page.on('pageerror', error => { errors.push(error.message); console.error('PAGE', error.message) })
page.on('response', response => {
    if (response.url().startsWith(base)) calls.push({ path: new URL(response.url()).pathname, status: response.status() })
})
async function api(route, method = 'GET', body) {
    return page.evaluate(async ({ route, method, body, prefix }) => {
        const cookies = Object.fromEntries(document.cookie.split('; ').map(value => { const index = value.indexOf('='); return [value.slice(0, index), decodeURIComponent(value.slice(index + 1))] }))
        const response = await fetch(prefix + route, { method, credentials: 'same-origin', headers: {
            Accept: 'application/json', 'Content-Type': 'application/json', 'X-Requested-With': 'XMLHttpRequest',
            'X-Fn2Auth-Origin': 'same-origin', 'X-XSRF-TOKEN': cookies['XSRF-TOKEN'] || '',
        }, body: body === undefined ? undefined : JSON.stringify(body) })
        const text = await response.text()
        let data; try { data = JSON.parse(text) } catch { data = null }
        return { status: response.status, data }
    }, { route, method, body, prefix: url.pathname.replace(/\/$/, '') })
}
async function check(name, run) {
    try { await run(); results.push({ name, passed: true }); console.log('PASS', name) }
    catch (error) { results.push({ name, passed: false, error: error.stack }); console.error('FAIL', name, error.message); throw error }
}
let account
try {
    await check('Real Go/Unix/PHP root, config and user session initialize the complete app', async () => {
        const response = await page.goto(base)
        assert.equal(response.status(), 200)
        await page.getByTestId('qr-image-input').waitFor()
        const config = await api('/_fn2auth/config')
        assert.equal(config.status, 200); assert.equal(config.data.user.username, 'sun')
        const user = await api('/api/v1/user')
        assert.equal(user.status, 200); assert.equal(user.data.is_admin, false)
        assert.ok((await page.locator('#btnEmailMenu').textContent()).includes('sun'))
        // Only the synthetic test user's preference is changed; no remote logos are needed.
        const preference = await api('/api/v1/user/preferences/getOfficialIcons', 'PUT', { value: false })
        assert.equal(preference.status, 201); assert.equal(preference.data.value, false)
        await page.screenshot({ path: path.join(output, 'native-start.png'), fullPage: true })
    })
    await check('Real PHP validates a local QR and saves only after the visible confirmation', async () => {
        await page.goto(new URL('capture', base).href)
        await page.getByTestId('qr-image-input').setInputFiles(path.resolve(imageArg))
        await page.waitForURL('**/account/create')
        await page.locator('.modal-otp.is-active').waitFor()
        let list = await api('/api/v1/twofaccounts')
        assert.equal(list.status, 200); assert.equal(list.data.length, 0)
        await page.screenshot({ path: path.join(output, 'native-qr-preview.png'), fullPage: true })
        await page.locator('.modal-otp form button[type="submit"]').click()
        await page.waitForURL('**/accounts')
        list = await api('/api/v1/twofaccounts')
        assert.equal(list.status, 200); assert.equal(list.data.length, 1)
        account = list.data[0]
        assert.equal(account.service, 'Public fixture')
        await page.screenshot({ path: path.join(output, 'native-account.png'), fullPage: true })
    })
    await check('Real PHP account update and delete complete without crossing the user boundary', async () => {
        const updated = await api('/api/v1/twofaccounts/' + account.id, 'PUT', { ...account, account: 'updated@example.invalid', secret: 'JBSWY3DPEHPK3PXP' })
        assert.equal(updated.status, 200); assert.equal(updated.data.account, 'updated@example.invalid')
        const removed = await api('/api/v1/twofaccounts/' + account.id, 'DELETE')
        assert.ok([200, 204].includes(removed.status))
        const list = await api('/api/v1/twofaccounts')
        assert.equal(list.data.length, 0)
    })
} catch { process.exitCode = 1 }
finally {
    await writeFile(path.join(output, 'report.json'), JSON.stringify({ schema: 1,
        boundary: 'Real built Vue, local worker/WASM, Go HTTP and Unix peer, FrankenPHP/Laravel and SQLite; synthetic NAS identity, not a real fnOS host',
        base, results, errors, calls }, null, 2))
    await browser.close()
}
