/** Exercise backup confirmation and recovery in the real built Vue view. */
import assert from 'node:assert/strict'
import { prefix } from './fixture.mjs'

export async function backupChecks(page, app, check) {
    const open = async () => { await page.goto(app.url + prefix + '/backup'); await page.getByTestId('backup-page').waitFor() }
    const prepare = async () => {
        await page.getByTestId('backup-file').setInputFiles({ name: 'public-fixture.age', mimeType: 'application/octet-stream', buffer: Buffer.from('public encrypted-format fixture') })
        await page.getByTestId('backup-passphrase').fill('synthetic-passphrase')
    }
    await check('Encrypted backup export has a download and clears the passphrase', async () => {
        await open(); await page.getByTestId('backup-passphrase').fill('synthetic-passphrase')
        const pending = page.waitForEvent('download')
        await page.getByTestId('backup-export').click()
        const download = await pending
        assert.equal(download.suggestedFilename(), 'Fn2Auth-backup.age')
        await page.waitForFunction(() => document.querySelector('[data-testid="backup-passphrase"]').value === '')
    })
    await check('Backup preview requires separate confirmation and sends a token only', async () => {
        await open(); await prepare()
        const start = app.requests.length
        await page.getByTestId('backup-preview').click()
        await page.getByTestId('backup-confirmation').waitFor()
        assert.equal(app.requests.slice(start).filter(item => item.path.endsWith('/backup/import')).length, 0)
        assert.equal(await page.getByTestId('backup-passphrase').inputValue(), '')
        assert.ok((await page.getByTestId('backup-confirmation').textContent()).includes('Public HOTP'))
        await page.getByTestId('backup-confirm').click()
        await page.getByTestId('backup-result').waitFor()
        const request = app.requests.slice(start).find(item => item.path.endsWith('/backup/import'))
        assert.deepEqual(JSON.parse(request.body), { preview_token: 'fixture-token', confirm: true })
        assert.equal(request.marker, 'same-origin')
    })
    await check('Expired backup preview cannot repeat its token and provides a fresh-preview path', async () => {
        await open(); await prepare(); app.state.backupCode = 'preview_expired'
        const start = app.requests.length
        await page.getByTestId('backup-preview').click(); await page.getByTestId('backup-confirmation').waitFor()
        await page.getByTestId('backup-confirm').click(); await page.getByTestId('backup-error').waitFor()
        assert.equal(await page.getByTestId('backup-confirm').count(), 0)
        assert.equal(app.requests.slice(start).filter(item => item.path.endsWith('/backup/import')).length, 1)
        assert.ok(await page.getByTestId('backup-preview').isEnabled())
        app.state.backupCode = null
    })
    await check('Discarding a backup preview makes no import request', async () => {
        await open(); await prepare()
        const start = app.requests.length
        await page.getByTestId('backup-preview').click(); await page.getByTestId('backup-confirmation').waitFor()
        await page.getByTestId('backup-cancel').click()
        assert.equal(await page.getByTestId('backup-confirmation').count(), 0)
        assert.equal(app.requests.slice(start).filter(item => item.path.endsWith('/backup/import')).length, 0)
    })
}
