/** Local raster-to-string decoding; no OTP validation or network service. */
import { readBarcodesFromImageData, setZXingModuleOverrides } from 'zxing-wasm/reader'
import wasmURL from 'zxing-wasm/reader/zxing_reader.wasm?url'
import { checkImageDimensions } from './image-dimensions'

setZXingModuleOverrides({ locateFile: path => path.endsWith('.wasm') ? wasmURL : path })
let detector
let initialized = false

async function decode(pixels) {
    if (!initialized) {
        initialized = true
        try {
            if (typeof BarcodeDetector === 'function' && (await BarcodeDetector.getSupportedFormats()).includes('qr_code')) {
                detector = new BarcodeDetector({ formats: ['qr_code'] })
            }
        } catch { detector = null }
    }
    if (detector) {
        try {
            const result = await detector.detect(pixels)
            if (result[0]?.rawValue) return result[0].rawValue
        } catch { detector = null }
    }
    const result = await readBarcodesFromImageData(pixels, { formats: ['QRCode'], tryHarder: true, maxNumberOfSymbols: 1 })
    return result.find(item => typeof item.text === 'string' && item.text)?.text || ''
}

async function readFile(file) {
    if (!file?.size || file.size > 20 * 1024 * 1024) throw new Error('imageTooLarge')
    await checkImageDimensions(file)
    if (typeof createImageBitmap !== 'function' || typeof OffscreenCanvas !== 'function') throw new Error('decoder')
    let bitmap
    try { bitmap = await createImageBitmap(file) }
    catch { throw new Error('imageUnsupported') }
    try {
        if (bitmap.width * bitmap.height > 32 * 1024 * 1024 || Math.max(bitmap.width, bitmap.height) > 32768) {
            throw new Error('imageTooLarge')
        }
        for (const limit of [1536, 3072]) {
            const ratio = Math.min(1, limit / Math.max(bitmap.width, bitmap.height))
            const canvas = new OffscreenCanvas(Math.max(1, Math.round(bitmap.width * ratio)), Math.max(1, Math.round(bitmap.height * ratio)))
            const context = canvas.getContext('2d', { willReadFrequently: true })
            if (!context) throw new Error('decoder')
            context.fillStyle = '#fff'
            context.fillRect(0, 0, canvas.width, canvas.height)
            context.drawImage(bitmap, 0, 0, canvas.width, canvas.height)
            const value = await decode(context.getImageData(0, 0, canvas.width, canvas.height))
            canvas.width = 0; canvas.height = 0
            if (value) return value
            if (ratio === 1) break
        }
        throw new Error('notFound')
    } finally { bitmap.close() }
}

self.onmessage = async ({ data }) => {
    try {
        const value = data.file ? await readFile(data.file) : await decode(data.pixels)
        if (value.length > 65536) throw new Error('invalid')
        self.postMessage({ id: data.id, value })
    } catch (error) {
        const known = ['imageTooLarge', 'imageUnsupported', 'notFound', 'invalid', 'decoder']
        self.postMessage({ id: data.id, error: known.includes(error.message) ? error.message : 'decoder' })
    }
}
