import {useCallback, useEffect, useRef, useState} from 'react'
import {useTranslation} from 'react-i18next'
import {AddTags, ListTags, MergeModels, RemoveTag, SearchModels, SetFavorite} from '../../wailsjs/go/main/App'
import {catalog} from '../../wailsjs/go/models'
import {EventsOn} from '../../wailsjs/runtime/runtime'
import {Msg, toMsg, translateMsg} from '../i18n/msg'
import {formatNumber} from '../format'
import {ModelDetailPanel} from './ModelDetail'
import {TagInput} from './TagInput'
import {Thumb} from './Thumb'
import {activeFilters, emptyQuery, FilterPanel, Query} from './FilterPanel'
import {FavoriteStar} from './FavoriteStar'

const ROW_HEIGHT = 60
const TILE_WIDTH = 190 // minimum grid tile width; tiles stretch to fill the row
const TILE_HEIGHT = 250
const PAGE_SIZE = 100
const OVERSCAN = 8
const ROW_TAGS = 3

type ViewMode = 'list' | 'grid'

// The chosen view is a per-user convenience; storage may be unavailable.
function loadViewMode(): ViewMode {
    try {
        return localStorage.getItem('modelkeeper.view') === 'grid' ? 'grid' : 'list'
    } catch {
        return 'list'
    }
}

/** Model list with live search (H1), virtual scrolling (C3), selection and detail panel (H4). */
export function ModelsView() {
    const {t} = useTranslation()
    const [query, setQuery] = useState<Query>(emptyQuery)
    const [showFilters, setShowFilters] = useState(false)
    const [view, setView] = useState<ViewMode>(loadViewMode)
    const changeView = (v: ViewMode) => {
        setView(v)
        try {
            localStorage.setItem('modelkeeper.view', v)
        } catch {
            // not persisted
        }
    }
    const [total, setTotal] = useState(0)
    const [selected, setSelected] = useState<number | null>(null)
    const [multi, setMulti] = useState<Map<number, string>>(new Map()) // id -> name
    const [anchorIndex, setAnchorIndex] = useState<number | null>(null)
    const [error, setError] = useState<Msg | null>(null)
    const [version, setVersion] = useState(0) // bumps to reload after scans and edits
    const [allTags, setAllTags] = useState<catalog.TagCount[]>([])
    const refresh = useCallback(() => setVersion(v => v + 1), [])

    useEffect(() => {
        ListTags().then(setAllTags).catch(() => setAllTags([]))
    }, [version])

    // Page cache for the current query: page index -> rows.
    const pages = useRef(new Map<number, catalog.ModelRow[]>())
    const loading = useRef(new Set<number>())
    const queryRef = useRef('')
    const [, forceRender] = useState(0)

    const loadPage = useCallback((page: number) => {
        if (pages.current.has(page) || loading.current.has(page)) return
        loading.current.add(page)
        const key = queryRef.current
        SearchModels(catalog.ModelQuery.createFrom(JSON.parse(key)), page * PAGE_SIZE, PAGE_SIZE).then(res => {
            if (key !== queryRef.current) return // outdated
            pages.current.set(page, res.items)
            setTotal(res.total)
            forceRender(n => n + 1)
        }).catch(e => setError(toMsg(e))).finally(() => loading.current.delete(page))
    }, [])

    const listRef = useRef<HTMLDivElement>(null)
    const queryKey = JSON.stringify(query)
    const lastKey = useRef('')

    // New query (debounced while typing) or data changed: reset the cache.
    useEffect(() => {
        const handle = setTimeout(() => {
            queryRef.current = queryKey
            pages.current = new Map()
            loading.current = new Set()
            setError(null)
            loadPage(0)
            if (lastKey.current !== queryKey) {
                listRef.current?.scrollTo({top: 0})
                lastKey.current = queryKey
            }
            forceRender(n => n + 1)
        }, 120)
        return () => clearTimeout(handle)
    }, [queryKey, version, loadPage])

    // Reload when a scan or detection finishes.
    useEffect(() => EventsOn('scan:progress', (p: { running: boolean }) => {
        if (!p.running) refresh()
    }), [refresh])

    const [scrollTop, setScrollTop] = useState(0)
    const [height, setHeight] = useState(600)
    const [width, setWidth] = useState(800)
    useEffect(() => {
        const el = listRef.current
        if (!el) return
        const ro = new ResizeObserver(() => {
            setHeight(el.clientHeight)
            setWidth(el.clientWidth)
        })
        ro.observe(el)
        return () => ro.disconnect()
    }, [])

    // Layout: the list is a grid with one column (H3, C3).
    const cols = view === 'grid' ? Math.max(1, Math.floor(width / TILE_WIDTH)) : 1
    const itemH = view === 'grid' ? TILE_HEIGHT : ROW_HEIGHT
    const itemW = width / cols
    const rowCount = Math.ceil(total / cols)
    const firstRow = Math.max(0, Math.floor(scrollTop / itemH) - Math.ceil(OVERSCAN / cols))
    const lastRow = Math.min(rowCount - 1, Math.ceil((scrollTop + height) / itemH) + Math.ceil(OVERSCAN / cols))
    const first = firstRow * cols
    const last = Math.min(total - 1, (lastRow + 1) * cols - 1)
    useEffect(() => {
        if (total === 0) return
        for (let p = Math.floor(first / PAGE_SIZE); p <= Math.floor(last / PAGE_SIZE); p++) loadPage(p)
    }, [first, last, total, loadPage, version])

    const rowAt = (i: number) => pages.current.get(Math.floor(i / PAGE_SIZE))?.[i % PAGE_SIZE]

    const onRowClick = (i: number, row: catalog.ModelRow, e: React.MouseEvent) => {
        if (e.shiftKey && anchorIndex !== null) {
            const next = new Map(multi)
            for (let k = Math.min(anchorIndex, i); k <= Math.max(anchorIndex, i); k++) {
                const r = rowAt(k)
                if (r) next.set(r.id, r.name)
            }
            setMulti(next)
            return
        }
        if (e.ctrlKey || e.metaKey) {
            const next = new Map(multi)
            if (next.size === 0 && selected !== null && selected !== row.id) {
                const cur = rowAt(anchorIndex ?? -1)
                if (cur) next.set(cur.id, cur.name)
            }
            if (next.has(row.id)) next.delete(row.id)
            else next.set(row.id, row.name)
            setMulti(next)
            setAnchorIndex(i)
            return
        }
        setMulti(new Map())
        setSelected(row.id)
        setAnchorIndex(i)
    }

    const rows = []
    for (let i = first; i <= last; i++) {
        const row = rowAt(i)
        const isSel = row && (multi.size > 0 ? multi.has(row.id) : row.id === selected)
        const style = view === 'grid'
            ? {top: Math.floor(i / cols) * itemH, left: (i % cols) * itemW, width: itemW, height: itemH}
            : {top: i * itemH, left: 0, right: 0, height: itemH}
        rows.push(
            <div key={i} className={`${view === 'grid' ? 'model-tile' : 'model-row'}${isSel ? ' selected' : ''}`}
                 style={style} onClick={e => row && onRowClick(i, row, e)}>
                {row ? (view === 'grid' ? <ModelTileView row={row} onChanged={refresh}/> : <ModelRowView row={row} onChanged={refresh}/>)
                    : <span className="muted">…</span>}
            </div>,
        )
    }

    return (
        <div className="models">
            <div className="models-list">
                <div className="row search">
                    <input type="search" autoFocus placeholder={t('models.searchPlaceholder')} value={query.text}
                           onChange={e => setQuery(q => ({...q, text: e.target.value}))}/>
                    <button className={query.favorites ? 'active' : ''} aria-pressed={query.favorites}
                            title={t('favorites.showOnly')}
                            onClick={() => setQuery(q => ({...q, favorites: !q.favorites}))}>
                        {query.favorites ? '★' : '☆'} {t('favorites.title')}
                    </button>
                    <button className={showFilters || activeFilters(query) > 0 ? 'active' : ''}
                            onClick={() => setShowFilters(s => !s)} aria-expanded={showFilters}>
                        {t('filters.button')}{activeFilters(query) > 0 && ` (${activeFilters(query)})`}
                    </button>
                    <span className="muted small nowrap">{t('models.count', {count: total, n: formatNumber(total)})}</span>
                    <span className="segmented" role="group" aria-label={t('views.label')}>
                        {(['list', 'grid'] as ViewMode[]).map(v => (
                            <button key={v} className={view === v ? 'active' : ''} aria-pressed={view === v}
                                    onClick={() => changeView(v)}>{t(`views.${v}`)}</button>
                        ))}
                    </span>
                </div>
                {showFilters && <FilterPanel query={query} onChange={setQuery} allTags={allTags} version={version}/>}
                {multi.size > 1 && (
                    <SelectionBar selection={multi} allTags={allTags} onChanged={refresh}
                                  onMerged={target => {
                                      setMulti(new Map())
                                      setSelected(target)
                                      refresh()
                                  }}
                                  onClear={() => setMulti(new Map())}/>
                )}
                {error && <p className="error">{translateMsg(error)}</p>}
                <div className="vlist" ref={listRef} onScroll={e => setScrollTop(e.currentTarget.scrollTop)}>
                    <div style={{height: rowCount * itemH, position: 'relative'}}>{rows}</div>
                    {total === 0 && !error && (
                        <p className="muted center-text">
                            {query.favorites && !query.text && activeFilters(query) === 0 ? t('favorites.empty')
                                : query.text || query.favorites || activeFilters(query) > 0 ? t('models.noResults') : t('models.empty')}
                        </p>
                    )}
                </div>
            </div>
            {selected !== null && multi.size === 0 &&
                <ModelDetailPanel id={selected} version={version} allTags={allTags} onChanged={refresh}
                                  onClose={() => setSelected(null)}/>}
        </div>
    )
}

/** Actions for several selected models: tags (G2) and merge (F4d). */
function SelectionBar({selection, allTags, onChanged, onMerged, onClear}: {
    selection: Map<number, string>
    allTags: catalog.TagCount[]
    onChanged: () => void
    onMerged: (target: number) => void
    onClear: () => void
}) {
    const {t} = useTranslation()
    const ids = [...selection.keys()]
    const [target, setTarget] = useState<number>(ids[0])
    const [error, setError] = useState<Msg | null>(null)
    const [busy, setBusy] = useState(false)
    const [done, setDone] = useState<string | null>(null)
    useEffect(() => {
        if (!selection.has(target)) setTarget(ids[0])
    }, [selection]) // eslint-disable-line react-hooks/exhaustive-deps

    const run = async (fn: () => Promise<void>, doneText?: string) => {
        setBusy(true)
        setError(null)
        setDone(null)
        try {
            await fn()
            if (doneText) setDone(doneText)
        } catch (e) {
            setError(toMsg(e))
        } finally {
            setBusy(false)
        }
    }

    const merge = () => {
        if (!window.confirm(t('corrections.confirmMerge', {count: ids.length, name: selection.get(target)}))) return
        run(async () => {
            await MergeModels(target, ids.filter(id => id !== target))
            onMerged(target)
        })
    }

    return (
        <div className="selection-bar">
            <div className="row">
                <strong>{t('selection.count', {n: ids.length})}</strong>
                <button className="link" onClick={onClear}>{t('selection.clear')}</button>
                {done && <span className="small muted">{done}</span>}
            </div>
            <div className="row wrap">
                <button disabled={busy} onClick={() => run(async () => {
                    await SetFavorite(ids, true)
                    onChanged()
                }, t('favorites.added', {count: ids.length}))}>★ {t('favorites.addSelected')}</button>
                <button disabled={busy} onClick={() => run(async () => {
                    await SetFavorite(ids, false)
                    onChanged()
                }, t('favorites.removed', {count: ids.length}))}>{t('favorites.removeSelected')}</button>
            </div>
            <div className="row wrap">
                <TagInput allTags={allTags} placeholder={t('tags.addPlaceholder')} buttonLabel={t('tags.add')}
                          disabled={busy} onSubmit={names => run(async () => {
                    await AddTags(ids, names)
                    onChanged()
                }, t('tags.added', {count: ids.length, tags: names.join(', ')}))}/>
                <TagInput allTags={allTags} placeholder={t('tags.removePlaceholder')} buttonLabel={t('tags.removeButton')}
                          disabled={busy} onSubmit={names => run(async () => {
                    for (const n of names) await RemoveTag(ids, n)
                    onChanged()
                }, t('tags.removed', {count: ids.length, tags: names.join(', ')}))}/>
            </div>
            <div className="row">
                <label>
                    {t('corrections.mergeInto')}
                    <select value={target} onChange={e => setTarget(Number(e.target.value))}>
                        {ids.map(id => <option key={id} value={id}>{selection.get(id)}</option>)}
                    </select>
                </label>
                <button onClick={merge} disabled={busy}>{t('corrections.merge')}</button>
            </div>
            {error && <p className="error">{translateMsg(error)}</p>}
        </div>
    )
}

function ModelTileView({row, onChanged}: { row: catalog.ModelRow; onChanged: () => void }) {
    const {t} = useTranslation()
    return (
        <>
            <Thumb src={row.thumb} className="tile-thumb"/>
            <FavoriteStar id={row.id} on={row.favorite} onChanged={onChanged} className="tile-star"/>
            <div className="tile-text">
                <strong className="ellipsis" title={row.name}>{row.name}</strong>
                <span className="row small muted">
                    <span className="ellipsis">{row.creator || t('models.noCreator')}</span>
                    {row.variants.length > 0 && <span className="chip">{row.variants.join(' · ')}</span>}
                    {row.inactive && <span className="warn-text nowrap" title={t('models.inactiveHint')}>{t('models.inactive')}</span>}
                </span>
            </div>
        </>
    )
}

function ModelRowView({row, onChanged}: { row: catalog.ModelRow; onChanged: () => void }) {
    const {t} = useTranslation()
    return (
        <div className="row row-with-thumb">
            <FavoriteStar id={row.id} on={row.favorite} onChanged={onChanged}/>
            <Thumb src={row.thumb} className="row-thumb"/>
            <div className="row-body">
            <div className="row spread">
                <strong className="ellipsis">{row.name}</strong>
                <span className="row small">
                    {row.variants.map(v => <span key={v} className="chip">{v}</span>)}
                    {row.supports && <span className="badge">{t(`supports.${row.supports}`)}</span>}
                </span>
            </div>
            <div className="row small muted">
                <span className="ellipsis">{row.creator || t('models.noCreator')}</span>
                <span className="nowrap">· {t('models.files', {count: row.files, n: formatNumber(row.files)})}</span>
                {row.roots > 1 && <span className="nowrap">· {t('models.roots', {n: row.roots})}</span>}
                {row.missing > 0 && <span className="warn-text nowrap">· {t('models.missing', {n: row.missing})}</span>}
                {row.inactive && <span className="warn-text nowrap" title={t('models.inactiveHint')}>· {t('models.inactive')}</span>}
                {row.tags.slice(0, ROW_TAGS).map(tag => <span key={tag} className="chip tag">{tag}</span>)}
                {row.tags.length > ROW_TAGS && <span>+{row.tags.length - ROW_TAGS}</span>}
            </div>
            </div>
        </div>
    )
}
