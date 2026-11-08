// i18n setup (concept N1–N5).
// Every JSON file in ./locales is a language; its file name is the language
// code. Adding a language = dropping in one more file, no code changes.
import i18n from 'i18next'
import {initReactI18next} from 'react-i18next'

type LocaleFile = { meta: { name: string } } & Record<string, unknown>

const files = import.meta.glob<LocaleFile>('./locales/*.json', {eager: true, import: 'default'})

const resources: Record<string, { translation: LocaleFile }> = {}
export const languages: { code: string; name: string }[] = []

for (const [path, data] of Object.entries(files)) {
    const code = path.match(/([\w-]+)\.json$/)![1]
    resources[code] = {translation: data}
    languages.push({code, name: data.meta?.name ?? code})
}
languages.sort((a, b) => a.name.localeCompare(b.name))

export const fallbackLanguage = 'en'

/** Picks the best available language for the system locale. */
export function systemLanguage(): string {
    for (const tag of navigator.languages ?? [navigator.language]) {
        const lower = tag.toLowerCase()
        if (resources[lower]) return lower
        const base = lower.split('-')[0]
        if (resources[base]) return base
    }
    return fallbackLanguage
}

/** Resolves a configured language ("" = system) to an available one. */
export function resolveLanguage(configured: string): string {
    return configured && resources[configured] ? configured : systemLanguage()
}

i18n.use(initReactI18next).init({
    resources,
    lng: systemLanguage(),
    fallbackLng: fallbackLanguage,
    interpolation: {escapeValue: false}, // React escapes already
    returnNull: false,
})

export default i18n
