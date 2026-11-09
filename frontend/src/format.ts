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
