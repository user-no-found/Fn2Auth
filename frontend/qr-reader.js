/** A cancellable single-operation worker; termination stops the decoder itself. */
import { cancelled } from './operation'

export class QrReader {
    constructor() {
        this.worker = null
        this.pending = null
        this.sequence = 0
    }

    read(payload, { signal, timeout = 10000 } = {}) {
        if (signal?.aborted) return Promise.reject(cancelled())
        if (this.pending) return Promise.reject(new Error('QR decoder already busy'))
        try {
            this.worker ??= new Worker(new URL('./qr-worker.js', import.meta.url), { type: 'module' })
        } catch { return Promise.reject(Object.assign(new Error('decoder'), { code: 'decoder' })) }
        const id = ++this.sequence
        return new Promise((resolve, reject) => {
            const finish = (error, value) => {
                clearTimeout(timer)
                signal?.removeEventListener('abort', abort)
                this.pending = null
                if (error) reject(error)
                else resolve(value)
            }
            const abort = () => this.close(cancelled())
            const timer = setTimeout(() => this.close(Object.assign(new Error('decodeTimeout'), { code: 'decodeTimeout' })), timeout)
            this.pending = finish
            signal?.addEventListener('abort', abort, { once: true })
            this.worker.onmessage = ({ data }) => {
                if (data.id !== id || !this.pending) return
                if (data.error) this.close(Object.assign(new Error(data.error), { code: data.error }))
                else finish(null, data.value)
            }
            this.worker.onerror = () => this.close(Object.assign(new Error('decoder'), { code: 'decoder' }))
            try { this.worker.postMessage({ id, ...payload }) }
            catch { this.close(Object.assign(new Error('decoder'), { code: 'decoder' })) }
        })
    }

    close(error = cancelled()) {
        this.worker?.terminate()
        this.worker = null
        this.pending?.(error)
    }
}
