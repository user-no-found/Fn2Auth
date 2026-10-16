/** Map transport and reader errors to reviewed message keys. */
export function qrError(error) {
    const codes = ['imageTooLarge', 'imageUnsupported', 'notFound', 'invalid', 'decoder', 'decodeTimeout']
    if (codes.includes(error?.code)) return { key: 'fn2auth.camera.' + error.code }
    if (error?.code === 'ECONNABORTED') return { key: 'fn2auth.session.timeout' }
    if (error?.response?.status) return { key: 'fn2auth.session.failed', status: error.response.status }
    return { key: 'fn2auth.session.network' }
}
