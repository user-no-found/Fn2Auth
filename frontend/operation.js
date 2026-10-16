/** Bounded asynchronous waits with cancellation and late-resource disposal. */
export function cancelled() {
    return Object.assign(new Error('Cancelled'), { code: 'ERR_CANCELED' })
}

export function waitFor(operation, { signal, timeout, code, late } = {}) {
    if (signal?.aborted) {
        Promise.resolve(operation).then(value => late?.(value), () => {})
        return Promise.reject(cancelled())
    }
    return new Promise((resolve, reject) => {
        let done = false
        const finish = (action, value) => {
            if (done) return false
            done = true
            clearTimeout(timer)
            signal?.removeEventListener('abort', abort)
            action(value)
            return true
        }
        const abort = () => finish(reject, cancelled())
        const timer = setTimeout(() => finish(reject, Object.assign(new Error(code), { code })), timeout)
        signal?.addEventListener('abort', abort, { once: true })
        Promise.resolve(operation).then(value => {
            if (!finish(resolve, value)) late?.(value)
        }, error => finish(reject, error))
    })
}
