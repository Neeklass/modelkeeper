import {useCallback, useEffect, useState} from 'react'
import {useTranslation} from 'react-i18next'
import {
    AddRoot, CancelScan, ExportHashes, GetRootStats, ImportHashes, GetScanProgress, ListLastScanIssues, ListRemovedRoots, ListRoots, PickFolder, PurgeRoot, RedetectRoot, DeactivateRoot,
    SetRootCreatorLevel, StartFullCheck, StartScan,
} from '../../wailsjs/go/main/App'
import {catalog, scanner} from '../../wailsjs/go/models'
import {EventsOn} from '../../wailsjs/runtime/runtime'
import {Msg, toMsg, translateMsg} from '../i18n/msg'
import {formatBytes, formatDateTime, formatNumber} from '../format'
import {Query} from './FilterPanel'

/** Root folder management and scanning (F1–F3, F7). */
export function RootsPanel({onShowModels}: { onShowModels?: (q: Partial<Query>, label?: string) => void } = {}) {
    const {t} = useTranslation()
    const [roots, setRoots] = useState<catalog.Root[]>([])
    const [removed, setRemoved] = useState<catalog.RemovedRoot[]>([])
    const [progress, setProgress] = useState<scanner.Progress | null>(null)
    const [error, setError] = useState<Msg | null>(null)
    const [notice, setNotice] = useState<{ rootId: number; text: string } | null>(null)

    const reload = useCallback(() => {
        ListRoots().then(setRoots).catch(e => setError(toMsg(e)))
        ListRemovedRoots().then(setRemoved).catch(e => setError(toMsg(e)))
    }, [])

    useEffect(() => {
        reload()
        GetScanProgress().then(p => p && setProgress(p))
        const offProgress = EventsOn('scan:progress', (p: scanner.Progress) => {
            setProgress(p)
            if (!p.running) reload()
        })
        const offRoots = EventsOn('roots:changed', reload)
        return () => {
            offProgress()
            offRoots()
        }
    }, [reload])

    const run = (fn: () => Promise<unknown>) => {
        setError(null)
        setNotice(null)
        fn().catch(e => setError(toMsg(e)))
    }

    const exportHashes = (r: catalog.Root) => run(async () => {
        const res = await ExportHashes(r.id)
        if (!res) return // cancelled
        let text = t('share.exported', {count: res.files, n: formatNumber(res.files)})
        if (res.unhashed > 0) text += ' ' + t('share.exportedUnhashed', {count: res.unhashed, n: formatNumber(res.unhashed)})
        setNotice({rootId: r.id, text})
    })

    const importHashes = (r: catalog.Root) => run(async () => {
        const res = await ImportHashes(r.id)
        if (!res) return
        const parts = [t('share.imported', {count: res.files, n: formatNumber(res.files)})]
        if (res.prefix) parts.push(t('share.mapped', {folder: res.prefix.slice(1)}))
        if (res.archivesReread > 0) parts.push(t('share.archivesReread', {count: res.archivesReread, n: formatNumber(res.archivesReread)}))
        parts.push(res.scanStarted ? t('share.scanStarted') : t('share.scanLater'))
        setNotice({rootId: r.id, text: parts.join(' ')})
    })

    const addPath = async (path: string) => {
        const r = await AddRoot(path)
        if (r.reactivated) setNotice({rootId: r.id, text: t('roots.reactivated')})
        reload()
    }

    const addRoot = () => run(async () => {
        const path = await PickFolder()
        if (path) await addPath(path)
    })

    const deactivateRoot = (r: catalog.Root) => run(async () => {
        if (!window.confirm(t('roots.confirmDeactivate', {path: r.path}))) return
        await DeactivateRoot(r.id)
        reload()
    })

    const scanning = progress?.running ?? false

    return (
        <section className="card">
            <div className="row spread">
                <h2>{t('roots.title')}</h2>
                <button onClick={addRoot}>{t('roots.add')}</button>
            </div>
            {roots.length === 0 && <p className="muted">{t('roots.empty')}</p>}
            {roots.map(r => (
                <RootRow key={r.id} root={r} scanning={scanning}
                         progress={progress?.rootId === r.id ? progress : null}
                         onScan={() => run(() => StartScan(r.id))}
                         onFullCheck={() => run(async () => {
                             if (window.confirm(t(r.kind === 'nas' ? 'scan.fullCheckConfirmNas' : 'scan.fullCheckConfirm'))) {
                                 await StartFullCheck(r.id)
                             }
                         })}
                         onRedetect={() => run(() => RedetectRoot(r.id))}
                         onCreatorLevel={on => run(async () => {
                             await SetRootCreatorLevel(r.id, on)
                             reload()
                         })}
                         onCancel={() => CancelScan()}
                         onRemove={() => deactivateRoot(r)}
                         onExport={() => exportHashes(r)}
                         onImport={() => importHashes(r)}
                         notice={notice?.rootId === r.id ? notice.text : null}/>
            ))}
            {error && <p className="error">{translateMsg(error)}</p>}
            {removed.length > 0 && <RemovedRoots roots={removed}
                                                 onShowModels={onShowModels}
                                                 onReactivate={r => run(() => addPath(r.path))}
                                                 onPurged={reload}/>}
        </section>
    )
}

/** Deactivated root folders (E9): reactivate, or delete permanently from the catalog. */
function RemovedRoots({roots, onReactivate, onPurged, onShowModels}: {
    roots: catalog.RemovedRoot[]
    onShowModels?: (q: Partial<Query>, label?: string) => void
    onReactivate: (r: catalog.RemovedRoot) => void
    onPurged: () => void
}) {
    const {t} = useTranslation()
    const [open, setOpen] = useState(false)
    const [purging, setPurging] = useState<number | null>(null)

    return (
        <div className="removed-roots">
            <button className="link" onClick={() => setOpen(o => !o)} aria-expanded={open}>
                {open ? '▾' : '▸'} {t('roots.removedTitle', {count: roots.length})}
            </button>
            {open && <>
                <p className="small muted">{t('roots.removedHint')}</p>
                {roots.map(r => (
                    <div key={r.id} className="root removed">
                        <div className="row spread">
                            <div>
                                <code className="path">{r.path}</code>
                                <div className="small muted">
                                    {r.removedAt && t('roots.removedAt', {when: formatDateTime(r.removedAt)})}
                                    {' · '}{t('roots.removedFiles', {count: r.files, n: formatNumber(r.files)})}
                                    {r.orphaned > 0 && <span className="warn-text">
                                        {' · '}{t('roots.removedOrphaned', {count: r.orphaned, n: formatNumber(r.orphaned)})}
                                    </span>}
                                </div>
                            </div>
                            <div className="row">
                                <button onClick={() => onReactivate(r)}>{t('roots.reactivate')}</button>
                                <button className="danger" onClick={() => setPurging(p => p === r.id ? null : r.id)}>
                                    {t('roots.purge')}
                                </button>
                            </div>
                        </div>
                        {purging === r.id && <PurgeDialog root={r} onCancel={() => setPurging(null)}
                                                          onShowModels={onShowModels && (() => onShowModels({orphanedBy: r.id}, r.path))}
                                                          onDone={() => { setPurging(null); onPurged() }}/>}
                    </div>
                ))}
            </>}
        </div>
    )
}

/**
 * Confirmation for permanently deleting a deactivated root. With models that
 * would lose their last location, the user has to type their number.
 */
function PurgeDialog({root, onCancel, onDone, onShowModels}: {
    root: catalog.RemovedRoot
    onCancel: () => void
    onDone: () => void
    onShowModels?: () => void
}) {
    const {t} = useTranslation()
    const [typed, setTyped] = useState('')
    const [busy, setBusy] = useState(false)
    const [error, setError] = useState<Msg | null>(null)
    const n = root.orphaned
    const ok = n === 0 || typed.trim() === String(n)

    async function purge() {
        setBusy(true)
        setError(null)
        try {
            await PurgeRoot(root.id, n)
            onDone()
        } catch (e) {
            setError(toMsg(e))
            setBusy(false)
        }
    }

    return (
        <div className="purge-dialog" role="alertdialog" aria-labelledby={`purge-${root.id}`}>
            <p id={`purge-${root.id}`}><strong>{t('roots.purgeTitle', {path: root.path})}</strong></p>
            <p className="small">{t('roots.purgeExplain')}</p>
            {n > 0
                ? <>
                    <p className="warn-text">{t('roots.purgeOrphaned', {count: n, n: formatNumber(n)})}</p>
                    {onShowModels && <button className="link small" onClick={onShowModels}>{t('roots.purgeShowModels')}</button>}
                    <label>
                        {t('roots.purgeType', {n})}
                        <input type="text" inputMode="numeric" value={typed} autoFocus
                               onChange={e => setTyped(e.target.value)} disabled={busy}/>
                    </label>
                </>
                : <p className="small">{t('roots.purgeNoOrphans')}</p>}
            <div className="row">
                <button onClick={onCancel} disabled={busy}>{t('roots.purgeCancel')}</button>
                <button className="danger" onClick={purge} disabled={!ok || busy}>
                    {busy ? t('roots.purging') : t('roots.purgeConfirm')}
                </button>
            </div>
            {error && <p className="error">{translateMsg(error)}</p>}
        </div>
    )
}

function RootRow(props: {
    root: catalog.Root
    scanning: boolean
    progress: scanner.Progress | null
    onScan: () => void
    onFullCheck: () => void
    onRedetect: () => void
    onCreatorLevel: (on: boolean) => void
    onCancel: () => void
    onRemove: () => void
    onExport: () => void
    onImport: () => void
    notice: string | null
}) {
    const {t} = useTranslation()
    const {root, progress} = props
    const [stats, setStats] = useState<catalog.RootStats | null>(null)
    const [issues, setIssues] = useState<catalog.Issue[]>([])
    const [showIssues, setShowIssues] = useState(false)
    const running = progress?.running ?? false

    useEffect(() => {
        if (running) return
        GetRootStats(root.id).then(setStats).catch(() => setStats(null))
        ListLastScanIssues(root.id).then(setIssues).catch(() => setIssues([]))
    }, [root.id, running, root.lastScanAt])

    return (
        <div className="root">
            <div className="row spread">
                <div>
                    <code className="path">{root.path}</code>
                    <div className="row small muted">
                        <span className="badge">{t(`roots.kind.${root.kind}`)}</span>
                        <span className={`badge status-${root.status}`}>{t(`roots.status.${root.status}`)}</span>
                        {root.live && <span className="badge status-online" title={t('roots.liveHint')}>{t('roots.live')}</span>}
                        <span>{root.lastScanAt
                            ? t('roots.lastScan', {when: formatDateTime(root.lastScanAt)})
                            : t('roots.neverScanned')}</span>
                    </div>
                </div>
                <div className="row">
                    {running
                        ? <button onClick={props.onCancel}>{t('scan.cancel')}</button>
                        : <>
                            <button onClick={props.onScan} disabled={props.scanning}>{t('scan.start')}</button>
                            <button onClick={props.onFullCheck} disabled={props.scanning || !root.lastScanAt}
                                    title={t('scan.fullCheckHint')}>{t('scan.fullCheck')}</button>
                            <button onClick={props.onRedetect} disabled={props.scanning}>{t('scan.redetect')}</button>
                        </>}
                    <button onClick={props.onRemove} disabled={running}>{t('roots.deactivate')}</button>
                </div>
            </div>

            <label className="small">
                <input type="checkbox" checked={root.creatorLevel} disabled={props.scanning}
                       onChange={e => props.onCreatorLevel(e.target.checked)}/>
                {t('roots.creatorLevel')}
            </label>

            <div className="row small share-row">
                <span className="muted">{t('share.label')}:</span>
                <button className="link small" onClick={props.onExport} disabled={!root.lastScanAt || props.scanning}
                        title={t('share.exportHint')}>{t('share.export')}</button>
                <button className="link small" onClick={props.onImport} disabled={props.scanning}
                        title={t('share.importHint')}>{t('share.import')}</button>
            </div>
            {props.notice && <p className="small">{props.notice}</p>}

            {progress && <ScanProgressView p={progress}/>}

            {stats && !running && (
                <dl className="stats">
                    <Stat label={t('stats.files')} value={formatNumber(stats.files)}/>
                    <Stat label={t('stats.size')} value={formatBytes(stats.bytes)}/>
                    <Stat label={t('stats.duplicates')} value={formatNumber(stats.duplicateFiles)}/>
                    <Stat label={t('stats.archives')} value={formatNumber(stats.archives)}/>
                    <Stat label={t('stats.archiveMembers')} value={formatNumber(stats.archiveMembers)}/>
                    <Stat label={t('stats.archivesBroken')} value={formatNumber(stats.archivesBroken)}
                          warn={stats.archivesBroken > 0}/>
                    <Stat label={t('stats.archivesEncrypted')} value={formatNumber(stats.archivesEncrypted)}/>
                    <Stat label={t('stats.missing')} value={formatNumber(stats.missing)} warn={stats.missing > 0}/>
                    {stats.unhashed + stats.archivesPending > 0 &&
                        <Stat label={t('stats.pending')} value={formatNumber(stats.unhashed + stats.archivesPending)}/>}
                </dl>
            )}

            {issues.length > 0 && !running && (
                <div>
                    <button className="link" onClick={() => setShowIssues(s => !s)}>
                        {t('scan.issues', {count: issues.length})}
                    </button>
                    {showIssues && (
                        <ul className="issues">
                            {issues.map(i => <li key={i.id}>{translateMsg(i.msg)}</li>)}
                        </ul>
                    )}
                </div>
            )}
        </div>
    )
}

function Stat({label, value, warn}: { label: string; value: string; warn?: boolean }) {
    return (
        <div className={warn ? 'warn' : ''}>
            <dt>{label}</dt>
            <dd>{value}</dd>
        </div>
    )
}

function ScanProgressView({p}: { p: scanner.Progress }) {
    const {t} = useTranslation()
    if (!p.running) {
        return (
            <p className={`small ${p.status === 'done' && !p.hashChanged ? 'muted' : 'error'}`}>
                {t(p.mode === 'full' && p.status === 'done' ? 'scan.result.fullDone' : `scan.result.${p.status}`, {
                    new: formatNumber(p.filesNew), changed: formatNumber(p.filesChanged),
                    missing: formatNumber(p.filesMissing), hashed: formatNumber(p.hashDone),
                    archives: formatNumber(p.archivesDone),
                })}
                {p.imported > 0 && <> {t('share.resultImported', {count: p.imported, n: formatNumber(p.imported)})}</>}
                {p.hashChanged > 0 && <> {t('scan.hashChanged', {count: p.hashChanged, n: formatNumber(p.hashChanged)})}</>}
                {p.status === 'done' && p.modelsFound > 0 && <> {t('scan.modelsResult', {
                    n: formatNumber(p.modelsFound), created: formatNumber(p.modelsCreated),
                })}</>}
                {p.error && <> — {translateMsg(p.error)}</>}
            </p>
        )
    }
    const frac = p.bytesTotal > 0 ? p.bytesDone / p.bytesTotal : 0
    return (
        <div className="progress">
            <div className="row spread small">
                <strong>{p.mode === 'full' && <>{t('scan.fullCheck')}: </>}{t(`scan.phase.${p.phase}`)}</strong>
                {p.phase === 'walking' && <span>{t('scan.found', {count: p.filesSeen, n: formatNumber(p.filesSeen)})}</span>}
                {p.phase === 'hashing' && <span>{formatNumber(p.hashDone)} / {formatNumber(p.hashTotal)}
                    {' · '}{formatBytes(p.bytesDone)} / {formatBytes(p.bytesTotal)}</span>}
                {p.phase === 'archives' && <span>{formatNumber(p.archivesDone)} / {formatNumber(p.archivesTotal)}
                    {' · '}{formatBytes(p.bytesDone)} / {formatBytes(p.bytesTotal)}</span>}
                {p.phase === 'thumbnails' && <span>{formatNumber(p.thumbsDone)} / {formatNumber(p.thumbsTotal)}</span>}
            </div>
            {(p.phase === 'hashing' || p.phase === 'archives') && (
                <div className="bar"><div style={{width: `${Math.min(100, frac * 100).toFixed(1)}%`}}/></div>
            )}
            {p.phase === 'thumbnails' && p.thumbsTotal > 0 && (
                <div className="bar"><div style={{width: `${(100 * p.thumbsDone / p.thumbsTotal).toFixed(1)}%`}}/></div>
            )}
            <code className="small muted current">{p.current}</code>
        </div>
    )
}
