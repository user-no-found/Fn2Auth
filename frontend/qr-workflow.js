/** Route decoded text through upstream validation without committing accounts. */
import { httpClientFactory } from './http-client'
const api = httpClientFactory('api')

export async function previewCode(value, { signal, bus, router }) {
    if (typeof value !== 'string' || value.length > 65536) throw Object.assign(new Error('invalid'), { code: 'invalid' })
    if (/^otpauth:\/\/(totp|hotp)\//i.test(value)) {
        try { await api.post('/twofaccounts/preview', { uri: value }, { signal, returnError: true }) }
        catch (error) {
            if (error.response?.status === 422) error.code = 'invalid'
            throw error
        }
        if (signal.aborted) return
        bus.decodedUri = value
        await router.push({ name: 'createAccount' })
    } else if (/^otpauth-migration:\/\/offline\?data=/i.test(value)) {
        // The upstream import view calls its preview-only migration endpoint.
        if (signal.aborted) return
        bus.migrationUri = value
        await router.push({ name: 'importAccounts' })
    } else throw Object.assign(new Error('invalid'), { code: 'invalid' })
}
