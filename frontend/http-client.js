/** Preserve Laravel cookies/XSRF while never replaying failed business requests. */
import axios from 'axios'
import { basePath, settleFailure } from './session'

export const httpClientFactory = (endpoint = 'api') => {
    const prefix = basePath()
    const client = axios.create({
        baseURL: prefix + (endpoint === 'web' ? '/' : '/api/v1'),
        timeout: 30000, withCredentials: true,
        headers: { 'X-Requested-With': 'XMLHttpRequest', 'Content-Type': 'application/json',
            'X-Fn2Auth-Origin': 'same-origin' },
    })
    client.interceptors.request.use(config => {
        const target = new URL(client.getUri(config), window.location.href)
        if (target.origin !== location.origin || !target.pathname.startsWith(prefix + '/')) {
            throw new Error('Application request must remain under its gateway prefix')
        }
        return config
    })
    client.interceptors.response.use(response => response, error => settleFailure(error, axios))
    return client
}
