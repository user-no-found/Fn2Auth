/** Simulated camera lifecycle checks through the built capture view. */
import assert from 'node:assert/strict'
import { prefix } from './fixture.mjs'

export async function cameraChecks(browser, app, check) {
    async function setup(mode) {
        const page = await browser.newPage()
        page.setDefaultTimeout(10000)
        await page.addInitScript(mode => {
            const canvas = document.createElement('canvas')
            canvas.width = canvas.height = 64
            const context = canvas.getContext('2d')
            context.fillStyle = 'white'; context.fillRect(0, 0, 64, 64)
            const stream = canvas.captureStream(5)
            window.cameraFixture = { calls: [], stopped: 0, stream }
            for (const track of stream.getTracks()) {
                const original = track.stop.bind(track)
                track.stop = () => { window.cameraFixture.stopped++; original() }
            }
            Object.defineProperty(navigator.mediaDevices, 'getUserMedia', { value: constraints => {
                window.cameraFixture.calls.push(constraints)
                if (mode === 'late') return new Promise(resolve => { window.resolveCamera = () => resolve(stream) })
                return Promise.resolve(stream)
            } })
            if (mode === 'playback') HTMLMediaElement.prototype.play = () => new Promise(() => {})
            if (mode === 'worker') {
                window.workerFixture = { terminated: 0 }
                window.Worker = class { postMessage() {} terminate() { window.workerFixture.terminated++ } }
            }
        }, mode)
        await page.goto(app.url + prefix + '/capture')
        await page.getByTestId('camera-start').waitFor()
        return page
    }
    await check('Late camera permission is stopped after explicit cancellation', async () => {
        const page = await setup('late')
        try {
            assert.equal(await page.evaluate(() => cameraFixture.calls.length), 0)
            await page.getByTestId('camera-start').click()
            await page.waitForFunction(() => cameraFixture.calls.length === 1)
            await page.getByTestId('camera-stop').click()
            await page.evaluate(() => resolveCamera())
            await page.waitForFunction(() => cameraFixture.stopped === 1)
            const constraints = await page.evaluate(() => cameraFixture.calls[0])
            assert.equal(constraints.audio, false)
            assert.equal(constraints.video.facingMode.ideal, 'environment')
            assert.equal(await page.locator('video').evaluate(video => video.srcObject), null)
        } finally { await page.close() }
    })
    await check('Active camera stops all tracks when page becomes hidden', async () => {
        const page = await setup('active')
        try {
            await page.getByTestId('camera-start').click()
            await page.waitForFunction(() => document.querySelector('[data-testid="camera-status"]').dataset.state === 'scanning')
            await page.evaluate(() => {
                Object.defineProperty(document, 'hidden', { configurable: true, value: true })
                document.dispatchEvent(new Event('visibilitychange'))
            })
            assert.equal(await page.evaluate(() => cameraFixture.stopped), 1)
            assert.equal(await page.locator('video').evaluate(video => video.srcObject), null)
        } finally { await page.close() }
    })
    await check('Unresolved video playback times out and releases the stream', async () => {
        const page = await setup('playback')
        try {
            await page.clock.install()
            await page.getByTestId('camera-start').click()
            await page.waitForFunction(() => document.querySelector('[data-testid="camera-status"]').dataset.state === 'playback')
            await page.clock.fastForward(8100)
            await page.getByTestId('camera-error').waitFor()
            assert.equal(await page.evaluate(() => cameraFixture.stopped), 1)
            assert.ok(await page.getByTestId('camera-start').isVisible())
        } finally { await page.close() }
    })
    await check('Hung decoder is terminated on timeout and the stream is released', async () => {
        const page = await setup('worker')
        try {
            await page.clock.install()
            await page.getByTestId('camera-start').click()
            await page.waitForFunction(() => document.querySelector('[data-testid="camera-status"]').dataset.state === 'scanning')
            await page.clock.fastForward(11000)
            await page.getByTestId('camera-error').waitFor()
            assert.ok(await page.evaluate(() => workerFixture.terminated > 0))
            assert.equal(await page.evaluate(() => cameraFixture.stopped), 1)
        } finally { await page.close() }
    })
}
