/** Own camera acquisition, playback and local frame scanning for one view. */
import { waitFor } from './operation'
import { QrReader } from './qr-reader'

const release = stream => stream?.getTracks().forEach(track => track.stop())

export function cameraError(error) {
    if (error?.code) return error.code
    return ({ NotAllowedError: 'denied', SecurityError: 'denied', NotFoundError: 'missing',
        NotReadableError: 'busy', AbortError: 'busy', OverconstrainedError: 'constraints',
        NotSupportedError: 'unsupported' })[error?.name] || 'unknown'
}

export function createCameraSession({ video, onState, onError, onResult, mediaDevices = navigator.mediaDevices }) {
    let generation = 0
    let stream = null
    let controller = null
    let timer = null
    let reader = null
    let frameDeadline = 0
    const canvas = document.createElement('canvas')
    const context = canvas.getContext('2d', { willReadFrequently: true })
    function stop() {
        generation++
        clearTimeout(timer)
        controller?.abort(); controller = null
        reader?.close(); reader = null
        release(stream); stream = null
        video.pause(); video.srcObject = null
        canvas.width = 0; canvas.height = 0
    }
    async function start(deviceId = '') {
        stop()
        const attempt = generation
        const active = controller = new AbortController()
        try {
            if (!window.isSecureContext) throw Object.assign(new Error('insecure'), { code: 'insecure' })
            if (!mediaDevices?.getUserMedia || !context) throw Object.assign(new Error('unsupported'), { code: 'unsupported' })
            onState('permission')
            const acquired = await waitFor(mediaDevices.getUserMedia({ audio: false, video: deviceId
                ? { deviceId: { exact: deviceId } } : { facingMode: { ideal: 'environment' } } }),
            { signal: active.signal, timeout: 15000, code: 'permissionTimeout', late: release })
            if (attempt !== generation) { release(acquired); return }
            stream = acquired
            video.srcObject = stream
            onState('playback')
            await waitFor(video.play(), { signal: active.signal, timeout: 8000, code: 'playbackTimeout' })
            if (attempt !== generation) return
            reader = new QrReader()
            frameDeadline = performance.now() + 8000
            onState('scanning')
            void scan(attempt, active.signal)
        } catch (error) {
            if (attempt !== generation) return
            stop()
            if (error.code !== 'ERR_CANCELED') onError(cameraError(error))
        }
    }
    async function scan(attempt, signal) {
        try {
            if (attempt !== generation) return
            if (video.readyState >= 2 && video.videoWidth && video.videoHeight) {
                frameDeadline = performance.now() + 8000
                const ratio = Math.min(1, 960 / Math.max(video.videoWidth, video.videoHeight))
                canvas.width = Math.max(1, Math.round(video.videoWidth * ratio))
                canvas.height = Math.max(1, Math.round(video.videoHeight * ratio))
                context.drawImage(video, 0, 0, canvas.width, canvas.height)
                const value = await reader.read({ pixels: context.getImageData(0, 0, canvas.width, canvas.height) }, { signal })
                if (attempt !== generation) return
                if (value) { stop(); onResult(value); return }
            }
            if (attempt !== generation) return
            if (performance.now() > frameDeadline) throw Object.assign(new Error('No readable camera frame'), { code: 'playbackTimeout' })
            timer = setTimeout(() => { void scan(attempt, signal) }, 250)
        } catch (error) {
            if (attempt !== generation) return
            stop()
            if (error.code !== 'ERR_CANCELED') onError(cameraError(error))
        }
    }
    return { start, stop }
}
