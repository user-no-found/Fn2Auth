/** Bound known raster headers before requesting a native bitmap decode. */
function dimensions(bytes) {
    const view = new DataView(bytes.buffer, bytes.byteOffset, bytes.byteLength)
    const text = (offset, count) => String.fromCharCode(...bytes.subarray(offset, offset + count))
    if (bytes.length >= 24 && text(1, 3) === 'PNG' && bytes[0] === 0x89 && text(12, 4) === 'IHDR') {
        return [view.getUint32(16), view.getUint32(20)]
    }
    if (bytes.length >= 10 && ['GIF87a', 'GIF89a'].includes(text(0, 6))) {
        return [view.getUint16(6, true), view.getUint16(8, true)]
    }
    if (bytes.length >= 30 && text(0, 4) === 'RIFF' && text(8, 4) === 'WEBP') {
        const type = text(12, 4)
        const uint24 = offset => bytes[offset] | bytes[offset + 1] << 8 | bytes[offset + 2] << 16
        if (type === 'VP8X') return [1 + uint24(24), 1 + uint24(27)]
        if (type === 'VP8 ' && text(23, 3) === '\x9d\x01\x2a') return [view.getUint16(26, true) & 16383, view.getUint16(28, true) & 16383]
        if (type === 'VP8L' && bytes[20] === 0x2f) {
            return [1 + (bytes[21] | (bytes[22] & 63) << 8),
                1 + ((bytes[22] >> 6) | bytes[23] << 2 | (bytes[24] & 15) << 10)]
        }
    }
    if (bytes[0] === 0xff && bytes[1] === 0xd8) {
        let offset = 2
        while (offset + 4 <= bytes.length) {
            if (bytes[offset++] !== 0xff) break
            while (bytes[offset] === 0xff) offset++
            const marker = bytes[offset++]
            if (marker === 0xda || marker === 0xd9) break
            if (marker === 0x01 || (marker >= 0xd0 && marker <= 0xd8)) continue
            if (offset + 2 > bytes.length) break
            const length = view.getUint16(offset)
            if (length < 2 || offset + length > bytes.length) break
            if ([0xc0, 0xc1, 0xc2, 0xc3, 0xc5, 0xc6, 0xc7, 0xc9, 0xca, 0xcb, 0xcd, 0xce, 0xcf].includes(marker) && length >= 7) {
                return [view.getUint16(offset + 5), view.getUint16(offset + 3)]
            }
            offset += length
        }
    }
    throw new Error('imageUnsupported')
}

export async function checkImageDimensions(file) {
    const bytes = new Uint8Array(await file.slice(0, 1024 * 1024).arrayBuffer())
    const [width, height] = dimensions(bytes)
    if (!width || !height) throw new Error('imageUnsupported')
    if (width * height > 32 * 1024 * 1024 || Math.max(width, height) > 32768) throw new Error('imageTooLarge')
    return { width, height }
}
