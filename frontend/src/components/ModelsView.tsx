import {useCallback, useEffect, useRef, useState} from 'react'
import {useTranslation} from 'react-i18next'
import {GetModel, SearchModels, ShowInExplorer} from '../../wailsjs/go/main/App'
import {catalog} from '../../wailsjs/go/models'
import {EventsOn} from '../../wailsjs/runtime/runtime'
import {Msg, toMsg, translateMsg} from '../i18n/msg'
import {formatBytes, formatNumber} from '../format'

const ROW_HEIGHT = 56
const PAGE_SIZE = 100
const OVERSCAN = 8

/** Model list with live search (H1), virtual scrolling (C3) and detail panel (H4). */
export function ModelsView() {
    const {t} = useTranslation()
    const [query, setQuery] = useState('')
    const [total, setTotal] = useState(0)
    const [selected, setSelected] = useState<number | null>(null)
    const [error, setError] = useState<Msg | null>(null)
    const [version, setVersion] = useState(0) // bumps to reload after scans

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

    // New query (debounced while typing) or data changed: reset the cache.
    useEffect(() => {
        const handle = setTimeout(() => {
            queryRef.current = query
            pages.current = new Map()
            loading.current = new Set()
            setError(null)
            loadPage(0)
            listRef.current?.scrollTo({top: 0})
        }, 120)
        return () => clearTimeout(handle)
    }, [query, version, loadPage])

    // Reload when a scan or detection finishes.
    useEffect(() => EventsOn('scan:progress', (p: { running: boolean }) => {
        if (!p.running) setVersion(v => v + 1)
    }), [])

    const listRef = useRef<HTMLDivElement>(null)
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
    }, [first, last, total, loadPage])

    const rows = []
    for (let i = first; i <= last; i++) {
        const row = pages.current.get(Math.floor(i / PAGE_SIZE))?.[i % PAGE_SIZE]
        rows.push(
            <div key={i} className={`model-row${row && row.id === selected ? ' selected' : ''}`}
                 style={{top: i * ROW_HEIGHT, height: ROW_HEIGHT}}
                 onClick={() => row && setSelected(row.id)}>
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
                {error && <p className="error">{translateMsg(error)}</p>}
                <div className="vlist" ref={listRef} onScroll={e => setScrollTop(e.currentTarget.scrollTop)}>
                    <div style={{height: total * ROW_HEIGHT, position: 'relative'}}>{rows}</div>
                    {total === 0 && !error && (
                        <p className="muted center-text">{query ? t('models.noResults') : t('models.empty')}</p>
                    )}
                </div>
            </div>
            {selected !== null && <ModelDetailPanel id={selected} version={version} onClose={() => setSelected(null)}/>}
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

function ModelDetailPanel({id, version, onClose}: { id: number; version: number; onClose: () => void }) {
    const {t} = useTranslation()
    const [d, setD] = useState<catalog.ModelDetail | null>(null)
    const [error, setError] = useState<Msg | null>(null)

    useEffect(() => {
        setError(null)
        GetModel(id).then(setD).catch(e => setError(toMsg(e)))
    }, [id, version])

    if (error) return <aside className="detail"><p className="error">{translateMsg(error)}</p></aside>
    if (!d) return <aside className="detail"><p className="muted">{t('app.loading')}</p></aside>

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

function basename(p: string): string {
    const i = p.lastIndexOf('/')
    return i >= 0 ? p.slice(i + 1) : p
}
