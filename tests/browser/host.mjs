/** Simulated mobile bridge and iframe lifecycle against the actual SDK bundle. */
import assert from 'node:assert/strict'
import { prefix } from './fixture.mjs'

export async function hostChecks(browser, app, check) {
    await check('An iframe without a responding host bridge still renders usable camera entries', async () => {
        const page = await browser.newPage()
        try {
            await page.goto(app.url + '/frame')
            const frame = page.frameLocator('iframe')
            await frame.getByTestId('camera-start').waitFor()
            assert.ok(await frame.getByTestId('qr-photo-input').isEnabled())
            assert.ok(await frame.getByTestId('qr-image-input').isEnabled())
        } finally { await page.close() }
    })
    await check('Mobile SDK rereads presentation on foreground and reconnects after cached-page restore', async () => {
        const page = await browser.newPage({ userAgent: 'Mozilla/5.0 Android FNOS/1.2.0401 FNAppType/Android FNAppVer/1.34.0' })
        try {
            await page.addInitScript(() => {
                window.bridgeFixture = { calls: [], theme: 'dark' }
                window.flutter_inappwebview = { _platformReady: true, callHandler: async method => {
                    bridgeFixture.calls.push(method)
                    const result = method === 'getAppMessage' ? { appApi: 'v1.0' }
                        : { theme: bridgeFixture.theme, language: 'en', systemVersion: '1.2.0401', format: {} }
                    return JSON.stringify({ result: JSON.stringify(result) })
                } }
            })
            await page.goto(app.url + prefix + '/capture')
            await page.getByTestId('camera-start').waitFor()
            await page.waitForFunction(() => document.documentElement.dataset.theme === 'dark')
            await page.evaluate(() => {
                bridgeFixture.theme = 'light'
                document.dispatchEvent(new Event('visibilitychange'))
            })
            await page.waitForFunction(() => document.documentElement.dataset.theme === 'light')
            await page.evaluate(() => {
                window.dispatchEvent(new PageTransitionEvent('pagehide', { persisted: true }))
                bridgeFixture.theme = 'dark'
                window.dispatchEvent(new PageTransitionEvent('pageshow', { persisted: true }))
            })
            await page.waitForFunction(() => document.documentElement.dataset.theme === 'dark')
            const calls = await page.evaluate(() => bridgeFixture.calls)
            assert.ok(calls.filter(method => method === 'getAppMessage').length >= 2)
            assert.ok(calls.filter(method => method === 'getPlatformConfig').length >= 3)
            assert.ok(!calls.includes('$on'))
        } finally { await page.close() }
    })
}
