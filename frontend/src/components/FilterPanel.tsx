import {useEffect, useState} from 'react'
import {useTranslation} from 'react-i18next'
import {GetFilterOptions, ListRoots} from '../../wailsjs/go/main/App'
import {catalog} from '../../wailsjs/go/models'

/** All list criteria (H1, H2, G4); mirrors catalog.ModelQuery. */
export interface Query {
    text: string
    untagged: boolean
    favorites: boolean
    tagId: number
    creatorId: number
    rootId: number
    ext: string
    supports: string
    printStatus: string
    duplicates: boolean
    singleLocation: boolean
    offline: boolean
    brokenArchives: boolean
    extractedAndArchive: boolean
    missing: boolean
    inactive: boolean
    /** Models without files in the catalog yet (imported, J7). */
    waiting: boolean
    orphanedBy: number
    /** "" = by name, "newest" = most recently added first. */
    sort: string
}

export const emptyQuery: Query = {
    text: '', untagged: false, favorites: false, tagId: 0, creatorId: 0, rootId: 0, ext: '', supports: '', printStatus: '',
    duplicates: false, singleLocation: false, offline: false, brokenArchives: false,
    extractedAndArchive: false, missing: false, inactive: false, waiting: false, orphanedBy: 0, sort: '',
}

export const FLAGS = ['duplicates', 'singleLocation', 'offline', 'brokenArchives', 'extractedAndArchive',
    'missing', 'inactive'] as const

/** Fields that choose the view (sidebar) rather than filter it. */
const VIEW_FIELDS = new Set(['text', 'favorites', 'untagged', 'sort', 'orphanedBy', 'waiting'])

/** Number of active filters, not counting the search text and the view (favorites, to catalog, sort). */
export function activeFilters(q: Query): number {
    let n = 0
    for (const [k, v] of Object.entries(q)) {
        if (!VIEW_FIELDS.has(k) && v !== (emptyQuery as unknown as Record<string, unknown>)[k]) n++
    }
    return n
}

/** The query without its filters; the search text and the view stay. */
export function clearFilters(q: Query): Query {
    const next = {...emptyQuery} as unknown as Record<string, unknown>
    for (const k of VIEW_FIELDS) next[k] = (q as unknown as Record<string, unknown>)[k]
    return next as unknown as Query
}

export function FilterPanel({query, onChange, allTags, version}: {
    query: Query
    onChange: (q: Query) => void
    allTags: catalog.TagCount[]
    version: number
}) {
    const {t} = useTranslation()
    const [opts, setOpts] = useState<catalog.FilterOptions | null>(null)
    const [roots, setRoots] = useState<catalog.Root[]>([])
    useEffect(() => {
        GetFilterOptions().then(setOpts).catch(() => setOpts(null))
        ListRoots().then(setRoots).catch(() => setRoots([]))
    }, [version])

    const set = <K extends keyof Query>(k: K, v: Query[K]) => onChange({...query, [k]: v})
    const num = (v: string) => Number(v) || 0

    return (
        <div className="filter-panel">
            <div className="filter-grid">
                <label>{t('filters.tag')}
                    <select value={query.tagId} onChange={e => set('tagId', num(e.target.value))}>
                        <option value={0}>{t('filters.any')}</option>
                        {allTags.map(tag => <option key={tag.id} value={tag.id}>{tag.name} ({tag.models})</option>)}
                    </select>
                </label>
                <label>{t('filters.creator')}
                    <select value={query.creatorId} onChange={e => set('creatorId', num(e.target.value))}>
                        <option value={0}>{t('filters.any')}</option>
                        {opts?.creators.map(c => <option key={c.id} value={c.id}>{c.name} ({c.models})</option>)}
                    </select>
                </label>
                <label>{t('filters.root')}
                    <select value={query.rootId} onChange={e => set('rootId', num(e.target.value))}>
                        <option value={0}>{t('filters.any')}</option>
                        {roots.map(r => <option key={r.id} value={r.id}>{r.path}</option>)}
                    </select>
                </label>
                <label>{t('filters.ext')}
                    <select value={query.ext} onChange={e => set('ext', e.target.value)}>
                        <option value="">{t('filters.any')}</option>
                        {opts?.exts.map(x => <option key={x.name} value={x.name}>.{x.name} ({x.models})</option>)}
                    </select>
                </label>
                <label>{t('models.supportsLabel')}
                    <select value={query.supports} onChange={e => set('supports', e.target.value)}>
                        <option value="">{t('filters.any')}</option>
                        {['presupported', 'unsupported', 'both', 'none'].map(s =>
                            <option key={s} value={s}>{t(`supports.${s}`)}</option>)}
                    </select>
                </label>
                <label>{t('models.printStatus')}
                    <select value={query.printStatus} onChange={e => set('printStatus', e.target.value)}>
                        <option value="">{t('filters.any')}</option>
                        {['not_printed', 'printed', 'failed'].map(s =>
                            <option key={s} value={s}>{t(`printStatus.${s}`)}</option>)}
                    </select>
                </label>
            </div>
            <div className="row wrap filter-flags">
                {FLAGS.map(f => (
                    <label key={f} className="small" title={t(`filters.${f}Hint`)}>
                        <input type="checkbox" checked={query[f]} onChange={e => set(f, e.target.checked)}/>
                        {t(`filters.${f}`)}
                    </label>
                ))}
            </div>
            {activeFilters(query) > 0 && (
                <button className="link small" onClick={() => onChange(clearFilters(query))}>
                    {t('filters.reset')}
                </button>
            )}
        </div>
    )
}
