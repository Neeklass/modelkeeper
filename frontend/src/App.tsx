import {useEffect, useState} from 'react'
import {useTranslation} from 'react-i18next'
import {CreateBackup, DismissNotice, GetStatus, OpenDataFolder, SetLanguage, SetNASRescanHours, StartWithEmptyCatalog} from '../wailsjs/go/main/App'
import {main} from '../wailsjs/go/models'
import {formatBytes, formatDateTime, formatNumber} from './format'
import i18n, {languages, resolveLanguage, systemLanguage} from './i18n'
import {Msg, toMsg, translateMsg} from './i18n/msg'
import {RootsPanel} from './components/RootsPanel'
import {ModelsView} from './components/ModelsView'
import {DetectionSettingsPanel} from './components/DetectionSettings'
import {BackupsPanel, backupReasonKey} from './components/BackupsPanel'

type Tab = 'models' | 'folders' | 'settings'

/** Intervals for scheduled NAS rescans in hours (App.NASRescanChoices), 0 = off. */
const NAS_RESCAN_HOURS = [0, 1, 6, 12, 24, 168]

function App() {
    const {t} = useTranslation()
    const [status, setStatus] = useState<main.AppStatus | null>(null)
    const [error, setError] = useState<Msg | null>(null)
    const [tab, setTab] = useState<Tab>('models')

    useEffect(() => {
        GetStatus().then(s => {
            i18n.changeLanguage(resolveLanguage(s.language))
            setStatus(s)
        }).catch(e => setError(toMsg(e)))
    }, [])

    if (!status) {
        return <div className="center">{error ? translateMsg(error) : t('app.loading')}</div>
    }

    if (status.startupError) {
        return <StartupError status={status} onStarted={setStatus}/>
    }

    return (
        <div className="shell">
            <header className="row">
                <h1>{t('app.title')}</h1>
                <nav className="tabs" role="tablist">
                    {(['models', 'folders', 'settings'] as Tab[]).map(k => (
                        <button key={k} role="tab" aria-selected={tab === k} className={tab === k ? 'active' : ''}
                                onClick={() => setTab(k)}>{t(`tabs.${k}`)}</button>
                    ))}
                </nav>
            </header>
            {status.notice && (
                <div className="row notice-inactive">
                    <span className="small">{translateMsg(status.notice as Msg)}</span>
                    <button className="link small" onClick={() => {
                        DismissNotice()
                        setStatus(main.AppStatus.createFrom({...status, notice: undefined}))
                    }}>{t('backup.dismiss')}</button>
                </div>
            )}
            {tab === 'models' && <ModelsView/>}
            {tab === 'folders' && <main><RootsPanel/></main>}
            {tab === 'settings' && <main>
                <SettingsPanel status={status} onChange={setStatus}/>
                <DetectionSettingsPanel/>
            </main>}
        </div>
    )
}

/** Shown instead of the app when the catalog could not be opened (J2: damaged or missing catalog). */
function StartupError({status, onStarted}: { status: main.AppStatus; onStarted: (s: main.AppStatus) => void }) {
    const {t} = useTranslation()
    const [error, setError] = useState<Msg | null>(null)
    const startup = status.startupError as Msg
    const restorable = startup.key === 'error.catalogCorrupt' || startup.key === 'error.catalogMissing'

    async function startEmpty() {
        if (!window.confirm(t('backup.confirmStartEmpty'))) return
        try {
            await StartWithEmptyCatalog()
            onStarted(await GetStatus())
        } catch (e) {
            setError(toMsg(e))
        }
    }

    return (
        <div className="center">
            <h1>{t('error.startupTitle')}</h1>
            <p className="error">{translateMsg(startup)}</p>
            {restorable && <p className="small">{t('backup.restoreHint')}</p>}
            <div className="row">
                {status.dataDir && <button onClick={() => OpenDataFolder().catch(e => setError(toMsg(e)))}>
                    {t('settings.openDataFolder')}
                </button>}
                {startup.key === 'error.catalogMissing' && <button onClick={startEmpty}>{t('backup.startEmpty')}</button>}
            </div>
            {status.dataDir && <BackupsPanel pending={status.pendingRestore}
                                             onChanged={() => GetStatus().then(onStarted).catch(e => setError(toMsg(e)))}/>}
            {error && <p className="error">{translateMsg(error)}</p>}
        </div>
    )
}

function SettingsPanel({status, onChange}: { status: main.AppStatus; onChange: (s: main.AppStatus) => void }) {
    const {t} = useTranslation()
    const [error, setError] = useState<Msg | null>(null)
    const [backupBusy, setBackupBusy] = useState(false)
    const [backupDone, setBackupDone] = useState<string | null>(null)

    async function createBackup() {
        setError(null)
        setBackupDone(null)
        setBackupBusy(true)
        try {
            const b = await CreateBackup()
            setBackupDone(b.path)
            onChange(await GetStatus())
        } catch (e) {
            setError(toMsg(e))
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

    return (
        <section className="card">
            <h2>{t('settings.title')}</h2>
            <label>
                {t('settings.language')}
                <select value={status.language} onChange={e => changeLanguage(e.target.value)}>
                    <option value="">{t('settings.languageSystem', {name: sysName})}</option>
                    {languages.map(l => <option key={l.code} value={l.code}>{l.name}</option>)}
                </select>
            </label>
            <label>
                {t('settings.nasRescan')}
                <select value={status.nasRescanHours} onChange={e => changeNASRescan(Number(e.target.value))}>
                    {NAS_RESCAN_HOURS.map(h => <option key={h} value={h}>{t(`settings.nasRescanEvery.${h}`)}</option>)}
                </select>
            </label>
            <p className="small muted">{t('settings.nasRescanHint')}</p>
            <div className="row">
                <span>{t('settings.dataFolder')}: <code>{status.dataDir}</code></span>
                <button onClick={() => OpenDataFolder().catch(e => setError(toMsg(e)))}>
                    {t('settings.openDataFolder')}
                </button>
                <button onClick={createBackup} disabled={backupBusy}>
                    {backupBusy ? t('backup.creating') : t('backup.create')}
                </button>
            </div>
            <p className="small muted">
                {status.lastBackup
                    ? <>
                        {t('backup.last', {when: formatDateTime(status.lastBackup.time), reason: t(backupReasonKey(status.lastBackup.reason))})}
                        {' '}{t('backup.total', {count: status.backups, n: formatNumber(status.backups), size: formatBytes(status.backupBytes)})}
                    </>
                    : t('backup.none')}
                {' '}{t('backup.hint')}
            </p>
            {backupDone && <p className="small">{t('backup.created', {path: backupDone})}</p>}
            <BackupsPanel pending={status.pendingRestore}
                          onChanged={() => GetStatus().then(onChange).catch(e => setError(toMsg(e)))}/>
            {error && <p className="error">{translateMsg(error)}</p>}
        </section>
    )
}

export default App
