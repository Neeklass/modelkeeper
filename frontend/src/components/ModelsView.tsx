import {useCallback, useEffect, useRef, useState} from 'react'
import {useTranslation} from 'react-i18next'
import {AddTags, MergeModels, RemoveTags, SearchModels, SetFavorite} from '../../wailsjs/go/main/App'
import {catalog} from '../../wailsjs/go/models'
import {Msg, toMsg, translateMsg} from '../i18n/msg'
import {formatNumber} from '../format'
import {Library} from '../library'
import {ModelDetailPanel} from './ModelDetail'
import {TagInput} from './TagInput'
import {Thumb} from './Thumb'
import {activeFilters, clearFilters, FilterPanel, FLAGS, Query} from './FilterPanel'
import {FavoriteStar} from './FavoriteStar'
import {useConfirm} from './ConfirmDialog'
import {useToast} from './Toast'
import {libraryView} from './Sidebar'

const ROW_HEIGHT = 64
const TILE_TEXT = 54 // height of the name and creator below a tile's picture
const TILE_SIZES = {s: 150, m: 210, l: 300} // minimum tile widths; tiles stretch to fill the row
const PAGE_SIZE = 100
const OVERSCAN = 8
const ROW_TAGS = 3

type ViewMode = 'list' | 'grid'
type TileSize = keyof typeof TILE_SIZES

// View and tile size are per-user conveniences; storage may be unavailable.
function load<T extends string>(key: string, allowed: readonly T[], fallback: T): T {
    try {
        const v = localStorage.getItem(key)
        return allowed.includes(v as T) ? v as T : fallback
    } catch {
        return fallback
    }
}

function store(key: string, v: string) {
    try {
        localStorage.setItem(key, v)
    } catch {
        // not persisted
    }
}

/** Typing in a field: keyboard navigation of the list stays out of the way. */
function typing(e: KeyboardEvent): boolean {
    const el = e.target as HTMLElement | null
    return !!el && (el.isContentEditable || ['INPUT', 'TEXTAREA', 'SELECT'].includes(el.tagName))
}

/**
 * The library: live search (H1), filters as chips (H2), grid of thumbnails or
 * list (H3) with virtual scrolling (C3), selection, keyboard navigation and
 * the detail panel (H4). In the "to catalog" view, a model that got its first
 * tag leaves the list and the next one is selected, so the backlog can be
 * worked through without the mouse (G4).
 */
export function ModelsView({query, onQuery, library, scopeLabel}: {
    query: Query
    onQuery: (q: Query) => void
    library: Library
    /** Describes query.orphanedBy (e.g. the folder about to be deleted). */
    scopeLabel?: string
}) {
    const {t} = useTranslation()
    const [showFilters, setShowFilters] = useState(false)
    const [view, setView] = useState<ViewMode>(() => load('modelkeeper.view', ['list', 'grid'] as const, 'grid'))
    const [size, setSize] = useState<TileSize>(() => load('modelkeeper.tile', ['s', 'm', 'l'] as const, 'm'))
    const changeView = (v: ViewMode) => {
        setView(v)
        store('modelkeeper.view', v)
    }
    const changeSize = (s: TileSize) => {
        setSize(s)
        store('modelkeeper.tile', s)
    }
    const [total, setTotal] = useState(0)
    const [loaded, setLoaded] = useState(false)
    const [selected, setSelected] = useState<number | null>(null)
    const [cursor, setCursor] = useState<number | null>(null) // list index of the selection
    const [multi, setMulti] = useState<Map<number, string>>(new Map()) // id -> name
    const [anchorIndex, setAnchorIndex] = useState<number | null>(null)
    const [error, setError] = useState<Msg | null>(null)
    const refresh = library.reload
    const version = library.version
    const set = (q: Partial<Query>) => onQuery({...query, ...q})

    // Page cache for the current query: page index -> rows.
    const pages = useRef(new Map<number, catalog.ModelRow[]>())
    const loading = useRef(new Set<number>())
    const queryRef = useRef('')
    const [tick, forceRender] = useState(0)

    const loadPage = useCallback((page: number) => {
        if (pages.current.has(page) || loading.current.has(page)) return
        loading.current.add(page)
        const key = queryRef.current
        SearchModels(catalog.ModelQuery.createFrom(JSON.parse(key)), page * PAGE_SIZE, PAGE_SIZE).then(res => {
            if (key !== queryRef.current) return // outdated
            pages.current.set(page, res.items)
            setTotal(res.total)
            setLoaded(true)
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
                setCursor(null)
                setMulti(new Map())
            }
            forceRender(n => n + 1)
        }, 120)
        return () => clearTimeout(handle)
    }, [queryKey, version, loadPage])

    const [scrollTop, setScrollTop] = useState(0)
    const [height, setHeight] = useState(600)
    const [width, setWidth] = useState(800)
    useEffect(() => {
        const el = listRef.current
        if (!el) return
        // The tiles are placed inside the padding: measure the content width.
        const measure = () => {
            const cs = getComputedStyle(el)
            setHeight(el.clientHeight)
            setWidth(el.clientWidth - (parseFloat(cs.paddingLeft) || 0) - (parseFloat(cs.paddingRight) || 0))
        }
        const ro = new ResizeObserver(measure)
        ro.observe(el)
        measure()
        return () => ro.disconnect()
    }, [view])

    // Layout: the list is a grid with one column (H3, C3).
    const cols = view === 'grid' ? Math.max(1, Math.floor(width / TILE_SIZES[size])) : 1
    const itemW = width / cols
    const itemH = view === 'grid' ? Math.round(itemW + TILE_TEXT) : ROW_HEIGHT
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

    // After a reload, find the selected model again. In the "to catalog" view
    // a model that got tags is gone: the one now at its place is selected.
    useEffect(() => {
        if (selected === null || cursor === null) return
        for (const [p, rows] of pages.current) {
            const k = rows.findIndex(r => r.id === selected)
            if (k >= 0) {
                if (p * PAGE_SIZE + k !== cursor) setCursor(p * PAGE_SIZE + k)
                return
            }
        }
        if (!query.untagged || !pages.current.has(Math.floor(cursor / PAGE_SIZE))) return
        const at = Math.min(cursor, total - 1)
        const next = at >= 0 ? rowAt(at) : undefined
        setSelected(next ? next.id : null)
        setCursor(next ? at : null)
    }, [tick]) // eslint-disable-line react-hooks/exhaustive-deps

    const scrollIntoView = (i: number) => {
        const el = listRef.current
        if (!el) return
        const top = Math.floor(i / cols) * itemH
        if (top < el.scrollTop) el.scrollTop = top
        else if (top + itemH > el.scrollTop + el.clientHeight) el.scrollTop = top + itemH - el.clientHeight
    }

    const select = (i: number) => {
        if (total === 0) return
        const k = Math.max(0, Math.min(total - 1, i))
        setMulti(new Map())
        setCursor(k)
        setAnchorIndex(k)
        scrollIntoView(k)
        const row = rowAt(k)
        if (row) setSelected(row.id)
        else {
            loadPage(Math.floor(k / PAGE_SIZE)) // selected once the page is there
            setPendingSelect(k)
        }
    }
    const [pendingSelect, setPendingSelect] = useState<number | null>(null)
    useEffect(() => {
        if (pendingSelect === null) return
        const row = rowAt(pendingSelect)
        if (row) {
            setSelected(row.id)
            setPendingSelect(null)
        }
    }, [tick, pendingSelect]) // eslint-disable-line react-hooks/exhaustive-deps

    const step = (d: number) => select(cursor === null ? 0 : cursor + d)
    const hasPrev = cursor !== null && cursor > 0
    const hasNext = cursor !== null ? cursor < total - 1 : total > 0

    // Keyboard: arrows move through the grid, Home/End, Esc closes, F favorite.
    useEffect(() => {
        const onKey = (e: KeyboardEvent) => {
            if (e.defaultPrevented || typing(e) || e.altKey || e.ctrlKey || e.metaKey) return
            if (document.querySelector('.modal-backdrop, .lightbox')) return
            const moves: Record<string, number> = {
                ArrowRight: 1, ArrowLeft: -1, ArrowDown: cols, ArrowUp: -cols, j: 1, k: -1,
                PageDown: cols * Math.max(1, Math.floor(height / itemH)), PageUp: -cols * Math.max(1, Math.floor(height / itemH)),
            }
            if (e.key in moves) {
                e.preventDefault()
                step(moves[e.key])
            } else if (e.key === 'Home') {
                e.preventDefault()
                select(0)
            } else if (e.key === 'End') {
                e.preventDefault()
                select(total - 1)
            } else if (e.key === 'Escape' && selected !== null) {
                setSelected(null)
            } else if ((e.key === 'f' || e.key === 'F') && cursor !== null) {
                const row = rowAt(cursor)
                if (row) SetFavorite([row.id], !row.favorite).then(refresh).catch(err => setError(toMsg(err)))
            }
        }
        window.addEventListener('keydown', onKey)
        return () => window.removeEventListener('keydown', onKey)
    })

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
        setCursor(i)
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
                 style={style} onClick={e => row && onRowClick(i, row, e)} role="option" aria-selected={!!isSel}>
                {row ? (view === 'grid' ? <ModelTileView row={row} onChanged={refresh}/> : <ModelRowView row={row} onChanged={refresh}/>)
                    : <div className={view === 'grid' ? 'tile-skeleton' : 'row-skeleton'}/>}
            </div>,
        )
    }

    const lv = libraryView(query)
    const nFilters = activeFilters(query)
    const searching = query.text.trim() !== ''

    return (
        <div className="models">
            <div className="models-list">
                <div className="toolbar">
                    <div className="toolbar-title">
                        <h1>{t(`nav.${lv}`)}</h1>
                        <span className="muted small nowrap">
                            {loaded ? t('models.count', {count: total, n: formatNumber(total)}) : t('app.loading')}
                        </span>
                    </div>
                    <div className="row toolbar-main">
                        <div className="search-box">
                            <span className="search-icon" aria-hidden="true">⌕</span>
                            <input type="search" autoFocus placeholder={t('models.searchPlaceholder')} value={query.text}
                                   aria-label={t('models.searchPlaceholder')}
                                   onChange={e => set({text: e.target.value})}
                                   onKeyDown={e => {
                                       if (e.key === 'ArrowDown' && total > 0) {
                                           e.preventDefault()
                                           ;(e.target as HTMLInputElement).blur()
                                           select(cursor ?? 0)
                                       }
                                   }}/>
                        </div>
                        <button className={`toggle${showFilters || nFilters > 0 ? ' active' : ''}`}
                                onClick={() => setShowFilters(s => !s)} aria-expanded={showFilters}>
                            ⚲ {t('filters.button')}{nFilters > 0 && ` (${nFilters})`}
                        </button>
                        {lv !== 'newest' && (
                            <label className="sort small">
                                <span className="muted">{t('sort.label')}</span>
                                <select value={query.sort} onChange={e => set({sort: e.target.value})}>
                                    <option value="">{searching ? t('sort.relevance') : t('sort.name')}</option>
                                    <option value="newest">{t('sort.newest')}</option>
                                </select>
                            </label>
                        )}
                        <span className="segmented" role="group" aria-label={t('views.label')}>
                            {(['grid', 'list'] as ViewMode[]).map(v => (
                                <button key={v} className={view === v ? 'active' : ''} aria-pressed={view === v}
                                        title={t(`views.${v}`)} onClick={() => changeView(v)}>
                                    {v === 'grid' ? '▦' : '☰'}
                                </button>
                            ))}
                        </span>
                        {view === 'grid' && (
                            <span className="segmented" role="group" aria-label={t('views.size')}>
                                {(['s', 'm', 'l'] as TileSize[]).map(s => (
                                    <button key={s} className={size === s ? 'active' : ''} aria-pressed={size === s}
                                            title={t(`views.size_${s}`)} onClick={() => changeSize(s)}>
                                        <span className={`size-dot size-${s}`} aria-hidden="true"/>
                                    </button>
                                ))}
                            </span>
                        )}
                    </div>
                    <FilterChips query={query} onQuery={onQuery} library={library}/>
                </div>
                {query.orphanedBy !== 0 && (
                    <div className="row notice-inactive small">
                        <span>{t('models.orphanedScope', {path: scopeLabel ?? ''})}</span>
                        <button className="link small" onClick={() => set({orphanedBy: 0})}>{t('models.orphanedScopeClear')}</button>
                    </div>
                )}
                {lv === 'untagged' && total > 0 && (
                    <div className="catalog-hint small">
                        {t('catalogMode.hint')}
                    </div>
                )}
                {showFilters && <FilterPanel query={query} onChange={onQuery} allTags={library.tags} version={version}/>}
                {multi.size > 1 && (
                    <SelectionBar selection={multi} allTags={library.tags} onChanged={refresh}
                                  onMerged={target => {
                                      setMulti(new Map())
                                      setSelected(target)
                                      refresh()
                                  }}
                                  onClear={() => setMulti(new Map())}/>
                )}
                {error && <p className="error pad">{translateMsg(error)}</p>}
                <div className={`vlist ${view}`} ref={listRef} role="listbox" aria-label={t(`nav.${lv}`)}
                     onScroll={e => setScrollTop(e.currentTarget.scrollTop)}>
                    <div style={{height: rowCount * itemH, position: 'relative'}}>{rows}</div>
                    {loaded && total === 0 && !error && <EmptyState query={query} library={library} onQuery={onQuery}/>}
                </div>
            </div>
            {selected !== null && multi.size === 0 &&
                <ModelDetailPanel id={selected} version={version} allTags={library.tags} onChanged={refresh}
                                  onClose={() => setSelected(null)}
                                  onPrev={hasPrev ? () => step(-1) : undefined}
                                  onNext={hasNext ? () => step(1) : undefined}
                                  position={cursor !== null ? {index: cursor, total} : undefined}
                                  cataloging={query.untagged}
                                  onShowCreator={name => {
                                      const c = library.creators.find(x => x.name === name)
                                      if (c) onQuery({...clearFilters(query), creatorId: c.id})
                                  }}/>}
        </div>
    )
}

/** The active filters as removable chips (H2). */
function FilterChips({query, onQuery, library}: { query: Query; onQuery: (q: Query) => void; library: Library }) {
    const {t} = useTranslation()
    const chips: { key: string; label: string; clear: Partial<Query> }[] = []
    if (query.creatorId) {
        const c = library.creators.find(x => x.id === query.creatorId)
        chips.push({key: 'creator', label: `${t('filters.creator')}: ${c?.name ?? '…'}`, clear: {creatorId: 0}})
    }
    if (query.tagId) {
        const tag = library.tags.find(x => x.id === query.tagId)
        chips.push({key: 'tag', label: `${t('filters.tag')}: ${tag?.name ?? '…'}`, clear: {tagId: 0}})
    }
    if (query.rootId) {
        const r = library.roots.find(x => x.id === query.rootId)
        chips.push({key: 'root', label: `${t('filters.root')}: ${r?.path ?? '…'}`, clear: {rootId: 0}})
    }
    if (query.ext) chips.push({key: 'ext', label: `.${query.ext}`, clear: {ext: ''}})
    if (query.supports) chips.push({key: 'supports', label: t(`supports.${query.supports}`), clear: {supports: ''}})
    if (query.printStatus) chips.push({key: 'print', label: t(`printStatus.${query.printStatus}`), clear: {printStatus: ''}})
    for (const f of FLAGS) {
        if (query[f]) chips.push({key: f, label: t(`filters.${f}`), clear: {[f]: false} as Partial<Query>})
    }
    if (chips.length === 0) return null
    return (
        <div className="row wrap filter-chips">
            {chips.map(c => (
                <span key={c.key} className="filter-chip">
                    <span className="ellipsis">{c.label}</span>
                    <button className="chip-x" aria-label={t('filters.remove', {name: c.label})}
                            onClick={() => onQuery({...query, ...c.clear})}>✕</button>
                </span>
            ))}
            {chips.length > 1 && <button className="link small" onClick={() => onQuery(clearFilters(query))}>{t('filters.reset')}</button>}
        </div>
    )
}

/** What to say (and offer) when the list is empty. */
function EmptyState({query, library, onQuery}: { query: Query; library: Library; onQuery: (q: Query) => void }) {
    const {t} = useTranslation()
    const lv = libraryView(query)
    const filtered = query.text.trim() !== '' || activeFilters(query) > 0
    let title = t('models.noResults')
    let text = ''
    if (!filtered) {
        if (library.counts.all === 0) {
            title = t('empty.libraryTitle')
            text = library.roots.length === 0 ? t('empty.noFolders') : t('empty.notScanned')
        } else if (lv === 'untagged') {
            title = t('empty.catalogedTitle')
            text = t('empty.cataloged')
        } else if (lv === 'favorites') {
            title = t('favorites.empty')
        }
    }
    return (
        <div className="empty-state">
            <div className="empty-icon" aria-hidden="true">{lv === 'untagged' && !filtered ? '✓' : '▦'}</div>
            <h2>{title}</h2>
            {text && <p className="muted">{text}</p>}
            {filtered && (
                <button onClick={() => onQuery({...clearFilters(query), text: ''})}>{t('empty.clearSearch')}</button>
            )}
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
    const confirm = useConfirm()
    const ids = [...selection.keys()]
    const [target, setTarget] = useState<number>(ids[0])
    const [error, setError] = useState<Msg | null>(null)
    const [busy, setBusy] = useState(false)
    const [done, setDone] = useState<string | null>(null)
    useEffect(() => {
        if (!selection.has(target)) setTarget(ids[0])
    }, [selection]) // eslint-disable-line react-hooks/exhaustive-deps

    const toast = useToast()
    // Bulk changes are undoable: the toast offers it (TODO 2.1).
    const run = async (fn: () => Promise<void>, doneText?: string) => {
        setBusy(true)
        setError(null)
        setDone(null)
        try {
            await fn()
            if (doneText) {
                setDone(doneText)
                toast.show({text: doneText, undo: true})
            }
        } catch (e) {
            setError(toMsg(e))
        } finally {
            setBusy(false)
        }
    }

    const merge = async () => {
        if (!await confirm({message: t('corrections.confirmMerge', {count: ids.length, name: selection.get(target)})})) return
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
                {done && <span className="small ok-text">{done}</span>}
            </div>
            <div className="row wrap">
                <TagInput allTags={allTags} placeholder={t('tags.addPlaceholder')} buttonLabel={t('tags.add')}
                          disabled={busy} onSubmit={names => run(async () => {
                    await AddTags(ids, names)
                    onChanged()
                }, t('tags.added', {count: ids.length, tags: names.join(', ')}))}/>
                <TagInput allTags={allTags} placeholder={t('tags.removePlaceholder')} buttonLabel={t('tags.removeButton')}
                          disabled={busy} onSubmit={names => run(async () => {
                    await RemoveTags(ids, names)
                    onChanged()
                }, t('tags.removed', {count: ids.length, tags: names.join(', ')}))}/>
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
        <div className="tile">
            <div className="tile-media">
                <Thumb src={row.thumb} className="tile-thumb" label={row.name}/>
                <FavoriteStar id={row.id} on={row.favorite} onChanged={onChanged} className="tile-star"/>
                <span className="tile-badges">
                    {row.inactive && <span className="tile-flag" title={t('models.inactiveHint')}>{t('models.rootRemoved')}</span>}
                    {row.missing > 0 && <span className="tile-flag warn" title={t('models.missing', {n: row.missing})}>!</span>}
                    {row.tags.length === 0 && <span className="tile-flag new" title={t('nav.untaggedHint')}>{t('models.untaggedBadge')}</span>}
                </span>
                {row.variants.length > 0 && <span className="tile-variants">{row.variants.join(' · ')}</span>}
            </div>
            <div className="tile-text">
                <strong className="ellipsis" title={row.name}>{row.name}</strong>
                <span className="ellipsis small muted">{row.creator || t('models.noCreator')}</span>
            </div>
        </div>
    )
}

function ModelRowView({row, onChanged}: { row: catalog.ModelRow; onChanged: () => void }) {
    const {t} = useTranslation()
    return (
        <div className="row row-with-thumb">
            <Thumb src={row.thumb} className="row-thumb" label={row.name}/>
            <div className="row-body">
                <div className="row spread">
                    <strong className="ellipsis">{row.name}</strong>
                    <span className="row small nowrap">
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
            <FavoriteStar id={row.id} on={row.favorite} onChanged={onChanged}/>
        </div>
    )
}
