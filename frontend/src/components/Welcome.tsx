import {useState} from 'react'
import {useTranslation} from 'react-i18next'
import {
    AddRoot, ChooseExternalBackupDir, FinishWelcome, ListBackupsIn, PickBackupFolder, PickFolder, Quit, RestoreFrom,
    SetLanguage, SetSyncDir, StartScan,
} from '../../wailsjs/go/main/App'
import {main} from '../../wailsjs/go/models'
import i18n, {languages, resolveLanguage, systemLanguage} from '../i18n'
import {Msg, toMsg, translateMsg} from '../i18n/msg'
import {formatDateTime, formatNumber} from '../format'
import {PathText} from './PathText'

type Step = 'language' | 'start' | 'restore' | 'folders' | 'backup' | 'tour'

/**
 * First-run guide (TODO 5.5): language, new start or the work from another
 * PC (backup folder, sync folder), model folders, a second backup folder and
 * a short tour. Every step can be skipped; nothing is changed without a click.
 */
export function Welcome({status, onDone}: { status: main.AppStatus; onDone: () => void }) {
    const {t} = useTranslation()
    const [step, setStep] = useState<Step>('language')
    const [lang, setLang] = useState(status.language)
    const [folders, setFolders] = useState<string[]>([])
    const [backupDir, setBackupDir] = useState(status.externalBackupDir)
    const [syncDir, setSyncDir] = useState('')
    const [restore, setRestore] = useState<{dir: string; list: main.BackupInfo[]} | null>(null)
    const [restored, setRestored] = useState(false)
    const [busy, setBusy] = useState(false)
    const [error, setError] = useState<Msg | null>(null)

    const run = async (fn: () => Promise<void>) => {
        setBusy(true)
        setError(null)
        try {
            await fn()
        } catch (e) {
            setError(toMsg(e))
        } finally {
            setBusy(false)
        }
    }
    const finish = () => run(async () => {
        await FinishWelcome()
        onDone()
    })
    const steps: Step[] = ['language', 'start', 'folders', 'backup', 'tour']
    const index = steps.indexOf(step === 'restore' ? 'start' : step)

    return (
        <div className="startup">
            <section className="card startup-card welcome">
                <div className="row spread">
                    <span className="small muted">{t('welcome.step', {n: index + 1, total: steps.length})}</span>
                    {step !== 'tour' && <button className="link small" onClick={finish} disabled={busy}>{t('welcome.skip')}</button>}
                </div>

                {step === 'language' && <>
                    <h1>{t('welcome.title')}</h1>
                    <p>{t('welcome.intro')}</p>
                    <label className="row">
                        {t('settings.language')}
                        <select value={lang} onChange={e => run(async () => {
                            const code = e.target.value
                            await SetLanguage(code)
                            await i18n.changeLanguage(resolveLanguage(code))
                            setLang(code)
                        })}>
                            <option value="">{t('settings.languageSystem', {name: languages.find(l => l.code === systemLanguage())?.name ?? systemLanguage()})}</option>
                            {languages.map(l => <option key={l.code} value={l.code}>{l.name}</option>)}
                        </select>
                    </label>
                    <p className="small muted">{t('welcome.safe')}</p>
                    <div className="row modal-actions">
                        <button className="primary" onClick={() => setStep('start')}>{t('welcome.next')}</button>
                    </div>
                </>}

                {step === 'start' && <>
                    <h1>{t('welcome.startTitle')}</h1>
                    <div className="welcome-choices">
                        <button className="welcome-choice" onClick={() => setStep('folders')}>
                            <b>{t('welcome.fresh')}</b><span className="small muted">{t('welcome.freshHint')}</span>
                        </button>
                        <button className="welcome-choice" disabled={busy} onClick={() => run(async () => {
                            const dir = await PickBackupFolder()
                            if (!dir) return
                            setRestore({dir, list: await ListBackupsIn(dir)})
                            setStep('restore')
                        })}>
                            <b>{t('welcome.fromBackup')}</b><span className="small muted">{t('welcome.fromBackupHint')}</span>
                        </button>
                        <button className="welcome-choice" disabled={busy} onClick={() => run(async () => {
                            const dir = await PickFolder()
                            if (!dir) return
                            await SetSyncDir(dir)
                            setSyncDir(dir)
                            setStep('folders')
                        })}>
                            <b>{t('welcome.fromSync')}</b><span className="small muted">{t('welcome.fromSyncHint')}</span>
                        </button>
                    </div>
                    {syncDir && <p className="small">{t('welcome.syncOn')} <PathText path={syncDir}/></p>}
                    <div className="row modal-actions">
                        <button onClick={() => setStep('language')}>{t('welcome.back')}</button>
                    </div>
                </>}

                {step === 'restore' && restore && <>
                    <h1>{t('welcome.restoreTitle')}</h1>
                    <p className="small muted">{t('backup.otherFolder', {path: restore.dir})}</p>
                    {restore.list.length === 0 && <p className="small">{t('welcome.noBackups')}</p>}
                    {restored ? <>
                        <p>{t('welcome.restoreDone')}</p>
                        <div className="row modal-actions">
                            <button className="primary" onClick={() => Quit()}>{t('backup.quitNow')}</button>
                        </div>
                    </> : <ul className="plain small backup-list">
                        {restore.list.slice(0, 15).map(b => (
                            <li key={b.name} className="row spread">
                                <span>{formatDateTime(b.time)} · {t('backup.counts', {models: formatNumber(b.models), tags: formatNumber(b.tags)})}</span>
                                <button className="link small" disabled={busy} onClick={() => run(async () => {
                                    await RestoreFrom(restore.dir, b.name)
                                    await FinishWelcome()
                                    setRestored(true)
                                })}>{t('backup.restore')}</button>
                            </li>
                        ))}
                    </ul>}
                    {!restored && <div className="row modal-actions">
                        <button onClick={() => setStep('start')}>{t('welcome.back')}</button>
                    </div>}
                </>}

                {step === 'folders' && <>
                    <h1>{t('welcome.foldersTitle')}</h1>
                    <p>{t('welcome.foldersIntro')}</p>
                    <ul className="plain small">
                        {folders.map(f => <li key={f}>✓ <PathText path={f}/></li>)}
                    </ul>
                    <button disabled={busy} onClick={() => run(async () => {
                        const path = await PickFolder()
                        if (!path) return
                        const r = await AddRoot(path)
                        setFolders(fs => [...fs, path])
                        await StartScan(r.id) // reading starts right away, in the background
                    })}>+ {t('welcome.addFolder')}</button>
                    <p className="small muted">{t('welcome.foldersHint')}</p>
                    <div className="row modal-actions">
                        <button onClick={() => setStep('start')}>{t('welcome.back')}</button>
                        <button className="primary" onClick={() => setStep('backup')}>
                            {folders.length ? t('welcome.next') : t('welcome.later')}
                        </button>
                    </div>
                </>}

                {step === 'backup' && <>
                    <h1>{t('welcome.backupTitle')}</h1>
                    <p>{t('welcome.backupIntro')}</p>
                    {backupDir
                        ? <p className="small">✓ <PathText path={backupDir}/></p>
                        : <button disabled={busy} onClick={() => run(async () => {
                            const dir = await ChooseExternalBackupDir()
                            if (dir) setBackupDir(dir)
                        })}>{t('backup.externalChoose')}</button>}
                    <p className="small muted">{t('welcome.backupHint')}</p>
                    <div className="row modal-actions">
                        <button onClick={() => setStep('folders')}>{t('welcome.back')}</button>
                        <button className="primary" onClick={() => setStep('tour')}>
                            {backupDir ? t('welcome.next') : t('welcome.later')}
                        </button>
                    </div>
                </>}

                {step === 'tour' && <>
                    <h1>{t('welcome.tourTitle')}</h1>
                    <ul className="welcome-tour">
                        <li>{t('welcome.tour1')}</li>
                        <li>{t('welcome.tour2')}</li>
                        <li>{t('welcome.tour3')}</li>
                        <li>{t('welcome.tour4')}</li>
                        <li>{t('welcome.tour5')}</li>
                    </ul>
                    <div className="row modal-actions">
                        <button onClick={() => setStep('backup')}>{t('welcome.back')}</button>
                        <button className="primary" onClick={finish} disabled={busy}>{t('welcome.finish')}</button>
                    </div>
                </>}

                {error && <p className="error">{translateMsg(error)}</p>}
            </section>
        </div>
    )
}
