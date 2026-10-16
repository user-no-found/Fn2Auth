/** Match complete browser/host language tags against installed translations. */
export function localeName(language, available) {
    const name = String(language || '').replaceAll('_', '-').toLowerCase()
    const exact = available.find(value => value.toLowerCase() === name)
    if (exact) return exact
    if (name.startsWith('zh')) return /hant|tw|hk/.test(name) ? 'zh-TW' : 'zh-CN'
    return available.includes(name.split('-')[0]) ? name.split('-')[0] : 'en'
}
