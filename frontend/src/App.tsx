import {useEffect, useState} from 'react'
import {useTranslation} from 'react-i18next'
import {
    CheckRestoreNow, ChooseExternalBackupDir, ChooseSlicer, ClearExternalBackupDir, ClearSlicer, CreateBackup, DismissNotice,
    GetStatus, OpenDataFolder, OpenExternalBackupDir, SetLanguage, SetNASRescanHours, StartWithEmptyCatalog,
} from '../wailsjs/go/main/App'
import {main} from '../wailsjs/go/models'
import {EventsOn} from '../wailsjs/runtime/runtime'
import {formatBytes, formatDateTime, formatNumber} from './format'
import i18n, {languages, resolveLanguage, systemLanguage} from './i18n'
import {Msg, toMsg, translateMsg} from './i18n/msg'
import {RootsPanel} from './components/RootsPanel'
import {ModelsView} from './components/ModelsView'
import {emptyQuery, Query} from './components/FilterPanel'
import {DetectionSettingsPanel} from './components/DetectionSettings'
import {BackupsPanel, backupReasonKey} from './components/BackupsPanel'
import {useConfirm} from './components/ConfirmDialog'
import {HelpView} from './components/HelpView'
import {Sidebar, View} from './components/Sidebar'
import {useLibrary} from './library'
import {CATALOG_CHANGED, useToast} from './components/Toast'
import {DataTransferPanel, HistoryPanel} from './components/DataTransfer'
import {TagManagerPanel} from './components/TagManager'

/** Typing in a field: Ctrl+Z belongs to the field then. */
function inField(e: KeyboardEvent): boolean {
    const el = e.target as HTMLElement | null
    return !!el && (el.isContentEditable || ['INPUT', 'TEXTAREA', 'SELECT'].includes(el.tagName))
}

/** Intervals for scheduled NAS rescans in hours (App.NASRescanChoices), 0 = off. */
const NAS_RESCAN_HOURS = [0, 1, 6, 12, 24, 168]

function App() {
    const {t} = useTranslation()
    const [status, setStatus] = useState<main.AppStatus | null>(null)
    const [error, setError] = useState<Msg | null>(null)
    const [view, setView] = useState<View>('library')
    const [query, setQuery] = useState<Query>(emptyQuery)
    const [scopeLabel, setScopeLabel] = useState<string | undefined>()
    const library = useLibrary()
    const toast = useToast()
    // Opening the library from elsewhere with a query (e.g. the folders view).
    const showModels = (q: Partial<Query>, label?: string) => {
        setQuery({...emptyQuery, ...q})
        setScopeLabel(label)
        setView('library')
    }

    // The start (restore, integrity check, migrations) runs in the background:
    // load the status when it is over (event), polling as a fallback.
    useEffect(() => {
        let done = false
        const load = () => GetStatus().then(s => {
            if (done || s.starting) return
            done = true
            i18n.changeLanguage(resolveLanguage(s.language))
            setStatus(s)
        }).catch(e => setError(toMsg(e)))
        const off = EventsOn('app:ready', load)
        load()
        const poll = setInterval(() => done ? clearInterval(poll) : load(), 500)
        return () => {
            done = true
            off()
            clearInterval(poll)
        }
    }, [])

    // Undo and imports change the catalog outside the normal flow.
    const reload = library.reload
    useEffect(() => {
        window.addEventListener(CATALOG_CHANGED, reload)
        return () => window.removeEventListener(CATALOG_CHANGED, reload)
    }, [reload])

    // Ctrl+Z undoes the newest change of user data (TODO 2.1), outside text fields.
    useEffect(() => {
        const onKey = (e: KeyboardEvent) => {
            if (!(e.ctrlKey || e.metaKey) || e.shiftKey || e.altKey || e.key.toLowerCase() !== 'z') return
            if (e.defaultPrevented || inField(e) || document.querySelector('.modal-backdrop')) return
            e.preventDefault()
            toast.undo()
        }
        window.addEventListener('keydown', onKey)
        return () => window.removeEventListener('keydown', onKey)
    }, [toast])

    // A correction made during a scan is applied when the scan is done.
    useEffect(() => EventsOn('corrections:queued', () => toast.show({text: t('corrections.queued')})), [toast, t])

    // Backup state changes in the background (external copy, restore test).
    useEffect(() => EventsOn('backups:changed', () => {
        GetStatus().then(s => !s.starting && setStatus(s)).catch(() => undefined)
    }), [])

    if (!status) {
        return <div className="center">{error ? translateMsg(error) : t('app.starting')}</div>
    }

    if (status.startupError) {
        return <StartupError status={status} onStarted={setStatus}/>
    }

    return (
        <div className="shell">
            <Sidebar view={view} onView={setView} query={query} library={library} status={status}
                     onQuery={q => {
                         setQuery(q)
                         setScopeLabel(undefined)
                         setView('library')
                     }}/>
            <div className="content">
                {status.locationWarning && (
                    <div className="row notice-warn" role="alert">
                        <span className="small">{translateMsg(status.locationWarning as Msg)}</span>
                    </div>
                )}
                {status.notice && (
                    <div className="row notice-inactive">
                        <span className="small">{translateMsg(status.notice as Msg)}</span>
                        <button className="link small" onClick={() => {
                            DismissNotice()
                            setStatus(main.AppStatus.createFrom({...status, notice: undefined}))
                        }}>{t('backup.dismiss')}</button>
                    </div>
                )}
                {view === 'library' && <ModelsView query={query} onQuery={setQuery} library={library} scopeLabel={scopeLabel}/>}
                {view === 'folders' && <main className="page">
                    <h1>{t('tabs.folders')}</h1>
                    <RootsPanel onShowModels={showModels}/>
                </main>}
                {view === 'settings' && <main className="page">
                    <h1>{t('tabs.settings')}</h1>
                    <SettingsPanel status={status} onChange={setStatus}/>
                    <DataTransferPanel/>
                    <HistoryPanel/>
                    <TagManagerPanel/>
                    <DetectionSettingsPanel/>
                </main>}
                {view === 'help' && <HelpView/>}
            </div>
        </div>
    )
}

/** Shown instead of the app when the catalog could not be opened (J2: damaged or missing catalog). */
function StartupError({status, onStarted}: { status: main.AppStatus; onStarted: (s: main.AppStatus) => void }) {
    const {t} = useTranslation()
    const confirm = useConfirm()
    const [error, setError] = useState<Msg | null>(null)
    const startup = status.startupError as Msg
    const restorable = startup.key === 'error.catalogCorrupt' || startup.key === 'error.catalogMissing'

    async function startEmpty() {
        if (!await confirm({message: t('backup.confirmStartEmpty'), confirmLabel: t('backup.startEmpty'), danger: true})) return
        try {
            await StartWithEmptyCatalog()
            onStarted(await GetStatus())
        } catch (e) {
            setError(toMsg(e))
        }
    }

    return (
        <div className="startup">
            <section className="card startup-card">
                <h1>{t('error.startupTitle')}</h1>
                <p className="error-box">{translateMsg(startup)}</p>
                <div className="row">
                    {status.dataDir && <button onClick={() => OpenDataFolder().catch(e => setError(toMsg(e)))}>
                        {t('settings.openDataFolder')}
                    </button>}
                    {startup.key === 'error.catalogMissing' &&
                        <button className="subtle" onClick={startEmpty}>{t('backup.startEmpty')}…</button>}
                </div>
                {restorable && status.dataDir && <>
                    <h2>{t('backup.restoreTitle')}</h2>
                    <BackupsPanel pending={status.pendingRestore} defaultOpen
                                  onChanged={() => GetStatus().then(onStarted).catch(e => setError(toMsg(e)))}/>
                    <details className="small muted">
                        <summary>{t('backup.restoreByHand')}</summary>
                        <p>{t('backup.restoreHint')}</p>
                    </details>
                </>}
                {error && <p className="error">{translateMsg(error)}</p>}
            </section>
        </div>
    )
}

function SettingsPanel({status, onChange}: { status: main.AppStatus; onChange: (s: main.AppStatus) => void }) {
    const {t} = useTranslation()
    const [error, setError] = useState<Msg | null>(null)
    const [backupBusy, setBackupBusy] = useState(false)
    const [backupDone, setBackupDone] = useState<string | null>(null)
    const [backupError, setBackupError] = useState<Msg | null>(null)

    async function createBackup() {
        setBackupError(null)
        setBackupDone(null)
        setBackupBusy(true)
        try {
            const b = await CreateBackup()
            setBackupDone(b.path)
            onChange(await GetStatus())
        } catch (e) {
            setBackupError(toMsg(e))
        } finally {
            setBackupBusy(false)
        }
    }
    const sysName = languages.find(l => l.code === systemLanguage())?.name ?? systemLanguage()

    async function changeLanguage(code: string) {
        try {
            await SetLanguage(code)
            await i18n.changeLanguage(resolveLanguage(code))
            onChange(main.AppStatus.createFrom({...status, language: code}))
        } catch (e) {
            setError(toMsg(e))
        }
    }

    async function changeNASRescan(hours: number) {
        try {
            await SetNASRescanHours(hours)
            onChange(main.AppStatus.createFrom({...status, nasRescanHours: hours}))
        } catch (e) {
            setError(toMsg(e))
        }
    }

    return (<>
        <section className="card">
            <h2>{t('settings.title')}</h2>
            <label>
                {t('settings.language')}
                <select value={status.language} onChange={e => changeLanguage(e.target.value)}>
                    <option value="">{t('settings.languageSystem', {name: sysName})}</option>
                    {languages.map(l => <option key={l.code} value={l.code}>{l.name}</option>)}
                </select>
            </label>
            <div>
                <label>
                    {t('settings.nasRescan')}
                    <select value={status.nasRescanHours} onChange={e => changeNASRescan(Number(e.target.value))}>
                        {NAS_RESCAN_HOURS.map(h => <option key={h} value={h}>{t(`settings.nasRescanEvery.${h}`)}</option>)}
                    </select>
                </label>
                <p className="small muted hint">{t('settings.nasRescanHint')}</p>
            </div>
            {error && <p className="error">{translateMsg(error)}</p>}
        </section>

        <SlicerSettings status={status} onChange={onChange}/>

        <section className="card">
            <h2>{t('backup.title')}</h2>
            <div className="row">
                <span className="muted">{t('settings.dataFolder')}:</span> <code>{status.dataDir}</code>
            </div>
            <div className="row">
                <button className="primary" onClick={createBackup} disabled={backupBusy}>
                    {backupBusy ? t('backup.creating') : t('backup.create')}
                </button>
                <button onClick={() => OpenDataFolder().catch(e => setBackupError(toMsg(e)))}>
                    {t('settings.openDataFolder')}
                </button>
            </div>
            <p className="small">
                {status.lastBackup
                    ? <>
                        {t('backup.last', {when: formatDateTime(status.lastBackup.time), reason: t(backupReasonKey(status.lastBackup.reason))})}
                        {' '}<span className="muted">{t('backup.total', {count: status.backups, n: formatNumber(status.backups), size: formatBytes(status.backupBytes)})}</span>
                    </>
                    : t('backup.none')}
            </p>
            {backupDone && <p className="small ok-text">{t('backup.created', {path: backupDone})}</p>}
            <BackupsPanel pending={status.pendingRestore}
                          onChanged={() => GetStatus().then(onChange).catch(e => setBackupError(toMsg(e)))}/>
            <p className="small muted hint">{t('backup.hint')}</p>
            {backupError && <p className="error">{translateMsg(backupError)}</p>}
        </section>

        <ExternalBackup status={status} onChange={onChange}/>
    </>)
}

/** Program for "Open in slicer" (H5). */
function SlicerSettings({status, onChange}: { status: main.AppStatus; onChange: (s: main.AppStatus) => void }) {
    const {t} = useTranslation()
    const [error, setError] = useState<Msg | null>(null)
    const run = (fn: () => Promise<unknown>) => {
        setError(null)
        fn().then(() => GetStatus()).then(onChange).catch(e => setError(toMsg(e)))
    }
    return (
        <section className="card">
            <h2>{t('slicer.title')}</h2>
            <div className="row">
                <span className="muted">{t('slicer.program')}:</span>
                {status.slicerPath ? <code>{status.slicerPath}</code> : <span>{t('slicer.systemDefault')}</span>}
            </div>
            <div className="row">
                <button onClick={() => run(ChooseSlicer)}>{t('slicer.choose')}</button>
                {status.slicerPath && <button className="subtle" onClick={() => run(ClearSlicer)}>{t('slicer.useDefault')}</button>}
            </div>
            <p className="small muted hint">{t('slicer.hint')}</p>
            {error && <p className="error">{translateMsg(error)}</p>}
        </section>
    )
}

/** Second backup folder and restore tests (J2): for when the PC breaks. */
function ExternalBackup({status, onChange}: { status: main.AppStatus; onChange: (s: main.AppStatus) => void }) {
    const {t} = useTranslation()
    const [error, setError] = useState<Msg | null>(null)
    const [busy, setBusy] = useState(false)
    const run = async (fn: () => Promise<unknown>) => {
        setError(null)
        setBusy(true)
        try {
            await fn()
            onChange(await GetStatus())
        } catch (e) {
            setError(toMsg(e))
        } finally {
            setBusy(false)
        }
    }
    const r = status.lastRestoreTest
    return (
        <section className="card">
            <h2>{t('backup.externalTitle')}</h2>
            <p className="small muted">{t('backup.externalIntro')}</p>
            <div className="row">
                <span className="muted">{t('backup.externalFolder')}:</span>
                {status.externalBackupDir ? <code>{status.externalBackupDir}</code> : <span className="warn-text">{t('backup.externalNone')}</span>}
            </div>
            <div className="row">
                <button className={status.externalBackupDir ? '' : 'primary'} disabled={busy}
                        onClick={() => run(ChooseExternalBackupDir)}>
                    {status.externalBackupDir ? t('backup.externalChange') : t('backup.externalChoose')}
                </button>
                {status.externalBackupDir && <>
                    <button disabled={busy} onClick={() => run(OpenExternalBackupDir)}>{t('backup.externalOpen')}</button>
                    <button className="subtle" disabled={busy} onClick={() => run(ClearExternalBackupDir)}>{t('backup.externalOff')}</button>
                </>}
            </div>

            <h3>{t('backup.checkTitle')}</h3>
            {r ? (
                <p className={`small ${r.ok ? 'ok-text' : 'error'}`}>
                    {r.ok
                        ? t(r.external ? 'backup.checkOkExternal' : 'backup.checkOk', {
                            when: formatDateTime(r.time), name: r.snapshot,
                            models: formatNumber(r.models), tags: formatNumber(r.tags),
                        })
                        : t('backup.checkFailed', {when: formatDateTime(r.time), problem: r.problem ?? ''})}
                </p>
            ) : <p className="small muted">{t('backup.checkNone')}</p>}
            <div className="row">
                <button disabled={busy} onClick={() => run(CheckRestoreNow)}>
                    {busy ? t('backup.checking') : t('backup.checkNow')}
                </button>
            </div>
            <p className="small muted hint">{t('backup.checkHint')}</p>
            {error && <p className="error">{translateMsg(error)}</p>}
        </section>
    )
}

export default App
