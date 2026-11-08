import {useCallback, useEffect, useRef, useState} from 'react'
import {useTranslation} from 'react-i18next'
import {
    DetachAnchor, GetModel, MarkFolderModel, MarkFolderNotModel, MergeModels, ResetCorrections, SearchModels,
    ShowInExplorer, SplitModel,
} from '../../wailsjs/go/main/App'
import {catalog} from '../../wailsjs/go/models'
import {EventsOn} from '../../wailsjs/runtime/runtime'
import {Msg, toMsg, translateMsg} from '../i18n/msg'
import {formatBytes, formatNumber} from '../format'

const ROW_HEIGHT = 56
const PAGE_SIZE = 100
const OVERSCAN = 8

/** Model list with live search (H1), virtual scrolling (C3), selection and detail panel (H4). */
export function ModelsView() {
    const {t} = useTranslation()
    const [query, setQuery] = useState('')
    const [total, setTotal] = useState(0)
    const [selected, setSelected] = useState<number | null>(null)
    const [multi, setMulti] = useState<Map<number, string>>(new Map()) // id -> name
    const [anchorIndex, setAnchorIndex] = useState<number | null>(null)
    const [error, setError] = useState<Msg | null>(null)
    const [version, setVersion] = useState(0) // bumps to reload after scans and corrections
    const refresh = useCallback(() => setVersion(v => v + 1), [])

    // Page cache for the current query: page index -> rows.
    const pages = useRef(new Map<number, catalog.ModelRow[]>())
    const loading = useRef(new Set<number>())
    const queryRef = useRef(query)
    const [, forceRender] = useState(0)

    const loadPage = useCallback((page: number) => {
        if (pages.current.has(page) || loading.current.has(page)) return
        loading.current.add(page)
        const q = queryRef.current
        SearchModels(q, page * PAGE_SIZE, PAGE_SIZE).then(res => {
            if (q !== queryRef.current) return // outdated
            pages.current.set(page, res.items)
            setTotal(res.total)
            forceRender(n => n + 1)
        }).catch(e => setError(toMsg(e))).finally(() => loading.current.delete(page))
    }, [])

    const listRef = useRef<HTMLDivElement>(null)
    const queryChanged = useRef(false)
    useEffect(() => { queryChanged.current = true }, [query])

    // New query (debounced while typing) or data changed: reset the cache.
    useEffect(() => {
        const handle = setTimeout(() => {
            queryRef.current = query
            pages.current = new Map()
            loading.current = new Set()
            setError(null)
            loadPage(0)
            if (queryChanged.current) {
                listRef.current?.scrollTo({top: 0})
                queryChanged.current = false
            }
            forceRender(n => n + 1)
        }, 120)
        return () => clearTimeout(handle)
    }, [query, version, loadPage])

    // Reload when a scan or detection finishes.
    useEffect(() => EventsOn('scan:progress', (p: { running: boolean }) => {
        if (!p.running) refresh()
    }), [refresh])

    const [scrollTop, setScrollTop] = useState(0)
    const [height, setHeight] = useState(600)
    useEffect(() => {
        const el = listRef.current
        if (!el) return
        const ro = new ResizeObserver(() => setHeight(el.clientHeight))
        ro.observe(el)
        return () => ro.disconnect()
    }, [])

    const first = Math.max(0, Math.floor(scrollTop / ROW_HEIGHT) - OVERSCAN)
    const last = Math.min(total - 1, Math.ceil((scrollTop + height) / ROW_HEIGHT) + OVERSCAN)
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
        rows.push(
            <div key={i} className={`model-row${isSel ? ' selected' : ''}`}
                 style={{top: i * ROW_HEIGHT, height: ROW_HEIGHT}}
                 onClick={e => row && onRowClick(i, row, e)}>
                {row ? <ModelRowView row={row}/> : <span className="muted">…</span>}
            </div>,
        )
    }

    return (
        <div className="models">
            <div className="models-list">
                <div className="row search">
                    <input type="search" autoFocus placeholder={t('models.searchPlaceholder')} value={query}
                           onChange={e => setQuery(e.target.value)}/>
                    <span className="muted small">{t('models.count', {count: total, n: formatNumber(total)})}</span>
                </div>
                {multi.size > 1 && (
                    <SelectionBar selection={multi} onDone={() => {
                        setMulti(new Map())
                        refresh()
                    }} onClear={() => setMulti(new Map())} onSelect={setSelected}/>
                )}
                {error && <p className="error">{translateMsg(error)}</p>}
                <div className="vlist" ref={listRef} onScroll={e => setScrollTop(e.currentTarget.scrollTop)}>
                    <div style={{height: total * ROW_HEIGHT, position: 'relative'}}>{rows}</div>
                    {total === 0 && !error && (
                        <p className="muted center-text">{query ? t('models.noResults') : t('models.empty')}</p>
                    )}
                </div>
            </div>
            {selected !== null && multi.size === 0 &&
                <ModelDetailPanel id={selected} version={version} onChanged={refresh}
                                  onClose={() => setSelected(null)}/>}
        </div>
    )
}

/** Actions for several selected models (merge; tags follow). */
function SelectionBar({selection, onDone, onClear, onSelect}: {
    selection: Map<number, string>
    onDone: () => void
    onClear: () => void
    onSelect: (id: number) => void
}) {
    const {t} = useTranslation()
    const ids = [...selection.keys()]
    const [target, setTarget] = useState<number>(ids[0])
    const [error, setError] = useState<Msg | null>(null)
    const [busy, setBusy] = useState(false)
    useEffect(() => {
        if (!selection.has(target)) setTarget(ids[0])
    }, [selection]) // eslint-disable-line react-hooks/exhaustive-deps

    const merge = async () => {
        if (!window.confirm(t('corrections.confirmMerge', {count: ids.length, name: selection.get(target)}))) return
        setBusy(true)
        setError(null)
        try {
            await MergeModels(target, ids.filter(id => id !== target))
            onSelect(target)
            onDone()
        } catch (e) {
            setError(toMsg(e))
        } finally {
            setBusy(false)
        }
    }

    return (
        <div className="selection-bar">
            <div className="row">
                <strong>{t('selection.count', {n: ids.length})}</strong>
                <button className="link" onClick={onClear}>{t('selection.clear')}</button>
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

function ModelRowView({row}: { row: catalog.ModelRow }) {
    const {t} = useTranslation()
    return (
        <>
            <div className="row spread">
                <strong className="ellipsis">{row.name}</strong>
                <span className="row small">
                    {row.variants.map(v => <span key={v} className="chip">{v}</span>)}
                    {row.supports && <span className="badge">{t(`supports.${row.supports}`)}</span>}
                </span>
            </div>
            <div className="row small muted">
                <span className="ellipsis">{row.creator || t('models.noCreator')}</span>
                <span>· {t('models.files', {count: row.files, n: formatNumber(row.files)})}</span>
                {row.roots > 1 && <span>· {t('models.roots', {n: row.roots})}</span>}
                {row.missing > 0 && <span className="warn-text">· {t('models.missing', {n: row.missing})}</span>}
            </div>
        </>
    )
}

function ModelDetailPanel({id, version, onChanged, onClose}: {
    id: number
    version: number
    onChanged: () => void
    onClose: () => void
}) {
    const {t} = useTranslation()
    const [d, setD] = useState<catalog.ModelDetail | null>(null)
    const [error, setError] = useState<Msg | null>(null)
    const [busy, setBusy] = useState(false)

    useEffect(() => {
        setError(null)
        GetModel(id).then(setD).catch(e => {
            const m = toMsg(e)
            if (m.key === 'error.modelNotFound') onClose() // merged or split away
            else setError(m)
        })
    }, [id, version]) // eslint-disable-line react-hooks/exhaustive-deps

    const act = async (fn: () => Promise<void>, confirmText?: string) => {
        if (confirmText && !window.confirm(confirmText)) return
        setBusy(true)
        setError(null)
        try {
            await fn()
            onChanged()
        } catch (e) {
            setError(toMsg(e))
        } finally {
            setBusy(false)
        }
    }

    if (!d) {
        return <aside className="detail">{error ? <p className="error">{translateMsg(error)}</p> :
            <p className="muted">{t('app.loading')}</p>}</aside>
    }

    const groups = new Map<string, catalog.ModelFile[]>()
    for (const f of d.files) {
        const key = f.variant || ''
        groups.set(key, [...(groups.get(key) ?? []), f])
    }

    return (
        <aside className="detail">
            <div className="row spread">
                <h2>{d.name}</h2>
                <button onClick={onClose} aria-label={t('models.close')}>✕</button>
            </div>
            {error && <p className="error">{translateMsg(error)}</p>}
            <dl className="props">
                <dt>{t('models.creator')}</dt><dd>{d.creator || '—'}</dd>
                <dt>{t('models.supportsLabel')}</dt><dd>{d.supports ? t(`supports.${d.supports}`) : '—'}</dd>
                {d.variants.length > 0 && <><dt>{t('models.variants')}</dt><dd>{d.variants.join(', ')}</dd></>}
                {d.tagSuggestions.length > 0 && <>
                    <dt>{t('models.tagSuggestions')}</dt>
                    <dd>{d.tagSuggestions.map(s => <span key={s} className="chip suggestion">{s}</span>)}</dd>
                </>}
            </dl>

            <h3>{t('models.locations')}</h3>
            <ul className="plain small">
                {d.anchors.map(a => (
                    <li key={a.rootId + a.path}>
                        <span className={`badge status-${a.online ? 'online' : 'offline'}`}>{t(`roots.kind.${a.rootKind}`)}</span>
                        {' '}<code>{a.rootPath}\{a.path.replaceAll('/', '\\')}</code>
                    </li>
                ))}
            </ul>

            <Corrections d={d} busy={busy} act={act}/>

            <h3>{t('models.filesTitle', {n: d.files.length})}</h3>
            {[...groups.entries()].map(([variant, files]) => (
                <div key={variant}>
                    {groups.size > 1 && <h4>{variant || t('models.noVariant')}</h4>}
                    <ul className="files">
                        {files.map(f => (
                            <li key={f.locationId} className={f.present ? '' : 'missing'}>
                                <div className="row spread">
                                    <span className="ellipsis" title={f.archivePath ? `${f.archivePath} › ${f.path}` : f.path}>
                                        {f.archivePath && <span className="muted">{basename(f.archivePath)} › </span>}
                                        {basename(f.path)}
                                    </span>
                                    <button className="link small" onClick={() => ShowInExplorer(f.locationId).catch(e => setError(toMsg(e)))}>
                                        {t('models.showInExplorer')}
                                    </button>
                                </div>
                                <div className="row small muted">
                                    <span>{f.ext.toUpperCase()}</span>
                                    <span>{formatBytes(f.size)}</span>
                                    {!f.present && <span className="warn-text">{t('models.fileMissing')}</span>}
                                    {f.copies > 1 && <span>{t('models.copies', {n: f.copies})}</span>}
                                    {f.archiveStatus && f.archiveStatus !== 'ok' &&
                                        <span className="warn-text">{t(`archiveStatus.${f.archiveStatus}`)}</span>}
                                </div>
                            </li>
                        ))}
                    </ul>
                </div>
            ))}
        </aside>
    )
}

/** Manual corrections of the detection (F4d): folder-level actions for each location. */
function Corrections({d, busy, act}: {
    d: catalog.ModelDetail
    busy: boolean
    act: (fn: () => Promise<void>, confirmText?: string) => void
}) {
    const {t} = useTranslation()
    const [open, setOpen] = useState(false)

    return (
        <>
            <h3>
                <button className="link" onClick={() => setOpen(o => !o)}>
                    {open ? '▾' : '▸'} {t('corrections.title')}
                </button>
            </h3>
            {open && (
                <div className="corrections">
                    <p className="small muted">{t('corrections.hint')}</p>
                    {d.anchors.map(a => {
                        const segs = a.path.split('/')
                        const minModelDepth = a.creatorLevel ? 2 : 1
                        const parent = segs.slice(0, -1).join('/')
                        const subs = a.kind === 'folder' ? subfolders(d.files, a) : []
                        return (
                            <div key={a.rootId + a.path} className="anchor-box">
                                <code className="small">{a.path}</code>
                                <div className="row wrap">
                                    {a.kind === 'folder' && segs.length - 1 >= minModelDepth && (
                                        <button disabled={busy} onClick={() => act(() => MarkFolderModel(a.rootId, parent))}
                                                title={t('corrections.parentIsModelHint', {name: segs[segs.length - 2]})}>
                                            {t('corrections.parentIsModel')}
                                        </button>
                                    )}
                                    {a.kind === 'folder' && segs.length > minModelDepth && (
                                        <button disabled={busy} onClick={() => act(() => MarkFolderNotModel(a.rootId, a.path))}>
                                            {t('corrections.notModel')}
                                        </button>
                                    )}
                                    {d.anchors.length > 1 && (
                                        <button disabled={busy} onClick={() => act(() => DetachAnchor(d.id, a.rootId, a.path))}>
                                            {t('corrections.detach')}
                                        </button>
                                    )}
                                </div>
                                {subs.length > 0 && (
                                    <ul className="plain small subfolders">
                                        {subs.map(s => (
                                            <li key={s.path} className="row spread">
                                                <span className="ellipsis">{s.name} <span className="muted">({s.files})</span></span>
                                                <button className="link" disabled={busy}
                                                        onClick={() => act(() => MarkFolderModel(a.rootId, s.path))}>
                                                    {t('corrections.ownModel')}
                                                </button>
                                            </li>
                                        ))}
                                    </ul>
                                )}
                            </div>
                        )
                    })}
                    <div className="row wrap">
                        {d.anchors.some(a => a.kind === 'folder' && subfolders(d.files, a).length > 1) && (
                            <button disabled={busy}
                                    onClick={() => act(() => SplitModel(d.id), t('corrections.confirmSplit'))}>
                                {t('corrections.split')}
                            </button>
                        )}
                        {d.hasCorrections && (
                            <button disabled={busy}
                                    onClick={() => act(() => ResetCorrections(d.id), t('corrections.confirmReset'))}>
                                {t('corrections.reset')}
                            </button>
                        )}
                    </div>
                </div>
            )}
        </>
    )
}

/** Direct subfolders of a folder anchor that contain files of the model. */
function subfolders(files: catalog.ModelFile[], a: catalog.ModelAnchor) {
    const prefix = a.path + '/'
    const counts = new Map<string, number>()
    for (const f of files) {
        if (f.archivePath || !f.present || f.rootId !== a.rootId || !f.path.startsWith(prefix)) continue
        const rest = f.path.slice(prefix.length)
        const i = rest.indexOf('/')
        if (i > 0) counts.set(rest.slice(0, i), (counts.get(rest.slice(0, i)) ?? 0) + 1)
    }
    return [...counts.entries()].sort().map(([name, n]) => ({name, path: prefix + name, files: n}))
}

function basename(p: string): string {
    const i = p.lastIndexOf('/')
    return i >= 0 ? p.slice(i + 1) : p
}
