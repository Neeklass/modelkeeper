import {useCallback, useEffect, useState} from 'react'
import {useTranslation} from 'react-i18next'
import {
    AddRoot, CancelScan, GetRootStats, GetScanProgress, ListLastScanIssues, ListRoots, PickFolder, RedetectRoot, RemoveRoot,
    SetRootCreatorLevel, StartFullCheck, StartScan,
} from '../../wailsjs/go/main/App'
import {catalog, scanner} from '../../wailsjs/go/models'
import {EventsOn} from '../../wailsjs/runtime/runtime'
import {Msg, toMsg, translateMsg} from '../i18n/msg'
import {formatBytes, formatDateTime, formatNumber} from '../format'

/** Root folder management and scanning (F1–F3, F7). */
export function RootsPanel() {
    const {t} = useTranslation()
    const [roots, setRoots] = useState<catalog.Root[]>([])
    const [progress, setProgress] = useState<scanner.Progress | null>(null)
    const [error, setError] = useState<Msg | null>(null)

    const reload = useCallback(() => {
        ListRoots().then(setRoots).catch(e => setError(toMsg(e)))
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
        fn().catch(e => setError(toMsg(e)))
    }

    const addRoot = () => run(async () => {
        const path = await PickFolder()
        if (!path) return
        await AddRoot(path)
        reload()
    })

    const removeRoot = (r: catalog.Root) => run(async () => {
        if (!window.confirm(t('roots.confirmRemove', {path: r.path}))) return
        await RemoveRoot(r.id)
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
                         onRemove={() => removeRoot(r)}/>
            ))}
            {error && <p className="error">{translateMsg(error)}</p>}
        </section>
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
                    <button onClick={props.onRemove} disabled={running}>{t('roots.remove')}</button>
                </div>
            </div>

            <label className="small">
                <input type="checkbox" checked={root.creatorLevel} disabled={props.scanning}
                       onChange={e => props.onCreatorLevel(e.target.checked)}/>
                {t('roots.creatorLevel')}
            </label>

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
