/** Same-origin public identity and user-visible, non-replaying request failures. */
import { reactive } from 'vue'
import branding from './branding.json'

export const sessionState = reactive({ error: null, username: '' })
let pending = null
let current = null

export function basePath() {
    return branding.gateway_prefix
}

export function presentFailure(error) {
    if (error?.code === 'ERR_CANCELED') return
    const status = error?.response?.status || error?.status || 0
    const key = error?.code === 'ECONNABORTED' || error?.name === 'TimeoutError' ? 'timeout'
        : error?.code === 'CONFIG_INVALID' ? 'invalid' : status ? 'failed' : 'network'
    sessionState.error = { key, status }
    error.fn2authPresented = true
}

export function publicConfig(force = false) {
    if (pending) return pending
    if (current && !force) return Promise.resolve(current)
    current = null
    pending = (async () => {
        const response = await fetch(basePath() + '/_fn2auth/config', {
            credentials: 'same-origin', cache: 'no-store', redirect: 'error',
            signal: AbortSignal.timeout(15000), headers: { Accept: 'application/json', 'X-Fn2Auth-Origin': 'same-origin' },
        })
        if (!response.ok) throw Object.assign(new Error('Session rejected'), { status: response.status })
        let data
        try { data = await response.json() }
        catch { throw Object.assign(new Error('Invalid public configuration'), { code: 'CONFIG_INVALID' }) }
        if (data?.schema !== 1 || data.gateway_prefix !== basePath() ||
            typeof data.user?.username !== 'string' || !data.user.username ||
            typeof data.display_name !== 'string' || typeof data.version !== 'string') {
            throw Object.assign(new Error('Invalid public configuration'), { code: 'CONFIG_INVALID' })
        }
        current = data
        sessionState.username = data.user.username
        return data
    })().finally(() => { pending = null })
    return pending
}

export async function settleFailure(error, client) {
    if (error?.code === 'ERR_CANCELED') throw error
    const status = error?.response?.status
    if ([401, 403, 419].includes(status)) {
        current = null
        if (status === 419) {
            try {
                await client.get(basePath() + '/refresh-csrf', { withCredentials: true, timeout: 15000,
                    headers: { 'X-Fn2Auth-Origin': 'same-origin' }, signal: error.config?.signal })
            } catch { /* The original request remains failed and is never replayed. */ }
        }
    }
    if (!error.config?.returnError && status !== 422) presentFailure(error)
    throw error
}
