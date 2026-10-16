/** Transport encrypted backups; the server owns validation, preview and import. */
import { httpClientFactory } from './http-client'
import branding from './branding.json'

const client = httpClientFactory('web')
const endpoint = '/_fn2auth/backup/'
const config = signal => ({ signal, returnError: true })

export async function exportBackup(passphrase, signal) {
    const response = await client.post(endpoint + 'export', { passphrase }, { ...config(signal), responseType: 'blob' })
    if (signal.aborted) return
    const url = URL.createObjectURL(response.data)
    const link = document.createElement('a')
    link.href = url
    link.download = branding.display_name + '-backup.age'
    link.click()
    setTimeout(() => URL.revokeObjectURL(url), 1000)
}

export async function previewBackup(file, passphrase, signal) {
    if (!file || !file.size || file.size > 8 * 1024 * 1024) throw Object.assign(new Error('fileInvalid'), { code: 'fileInvalid' })
    const bytes = new Uint8Array(await file.arrayBuffer())
    if (signal.aborted) throw Object.assign(new Error('Cancelled'), { code: 'ERR_CANCELED' })
    const chunks = []
    for (let offset = 0; offset < bytes.length; offset += 8192) chunks.push(String.fromCharCode(...bytes.subarray(offset, offset + 8192)))
    const response = await client.post(endpoint + 'preview', { passphrase, encrypted: btoa(chunks.join('')) }, config(signal))
    const data = response.data
    if (data?.schema !== 1 || typeof data.preview_token !== 'string' || !data.preview_token || !Array.isArray(data.accounts)) throw new Error('Invalid backup preview')
    return data
}

export async function importBackup(previewToken, signal) {
    const { data } = await client.post(endpoint + 'import', { preview_token: previewToken, confirm: true }, config(signal))
    if (data?.schema !== 1 || !['imported', 'skipped', 'updated_counters'].every(key => Number.isInteger(data[key]) && data[key] >= 0)) throw new Error('Invalid backup result')
    return data
}

export async function backupError(error) {
    let data = error.response?.data
    if (data instanceof Blob && data.size < 65536) {
        try { data = JSON.parse(await data.text()) } catch { data = null }
    }
    const keys = ['fileInvalid', 'backup_invalid_request', 'decryption_failed', 'preview_expired',
        'preview_conflict', 'backup_unavailable', 'session_required']
    const key = data?.code || error.code
    return 'fn2auth.backup.' + (keys.includes(key) ? key : 'failed')
}
