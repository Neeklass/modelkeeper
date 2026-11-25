import i18n from './i18n'

const units = ['B', 'KB', 'MB', 'GB', 'TB']

export function formatBytes(n: number): string {
    let i = 0
    while (n >= 1024 && i < units.length - 1) {
        n /= 1024
        i++
    }
    const nf = new Intl.NumberFormat(i18n.language, {maximumFractionDigits: i === 0 ? 0 : 1})
    return `${nf.format(n)} ${units[i]}`
}

export function formatNumber(n: number): string {
    return new Intl.NumberFormat(i18n.language).format(n)
}

export function formatDateTime(ms: number): string {
    return new Intl.DateTimeFormat(i18n.language, {dateStyle: 'medium', timeStyle: 'short'}).format(new Date(ms))
}

/** Duration in words: "2 h 05 min", "12 min", "< 1 min". */
export function formatDuration(secs: number): string {
    const m = Math.round(secs / 60)
    if (m < 1) return i18n.t('duration.lessThanMinute')
    if (m < 60) return i18n.t('duration.minutes', {m})
    const h = Math.floor(m / 60)
    if (h < 48) return i18n.t('duration.hours', {h, m: String(m % 60).padStart(2, '0')})
    return i18n.t('duration.days', {d: Math.floor(h / 24), h: h % 24})
}
