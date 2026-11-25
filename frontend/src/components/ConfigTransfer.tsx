import {useEffect, useState} from 'react'
import {useTranslation} from 'react-i18next'
import {
    ApplyConfig, ApplyMoveSuggestions, DismissMoved, ExportConfig, GetMoveSuggestions, PickConfigFile,
} from '../../wailsjs/go/main/App'
import {main} from '../../wailsjs/go/models'
import {Msg, toMsg, translateMsg} from '../i18n/msg'
import {formatDateTime, formatNumber} from '../format'
import {CATALOG_CHANGED} from './Toast'
import {PathText} from './PathText'

/**
 * The configuration (settings and folder list) as a JSON file: to set up
 * another PC or a fresh installation the same way (M).
 */
export function ConfigTransferPanel({onApplied}: { onApplied: () => void }) {
    const {t} = useTranslation()
    const [error, setError] = useState<Msg | null>(null)
    const [done, setDone] = useState<string | null>(null)
    const [preview, setPreview] = useState<main.ConfigPreview | null>(null)

    const exportNow = async () => {
        setError(null)
        setDone(null)
        try {
            const file = await ExportConfig()
            if (file) setDone(t('config.exported', {path: file}))
        } catch (e) {
            setError(toMsg(e))
        }
    }
    const pick = async () => {
        setError(null)
        setDone(null)
        try {
            const p = await PickConfigFile()
            if (p) setPreview(p)
        } catch (e) {
            setError(toMsg(e))
        }
    }

    return (
        <section className="card">
            <h2>{t('config.title')}</h2>
            <p className="small muted">{t('config.intro')}</p>
            <div className="row wrap">
                <button onClick={exportNow}>{t('config.export')}…</button>
                <button onClick={pick}>{t('config.import')}…</button>
            </div>
            {done && <p className="small ok-text">{done}</p>}
            {error && <p className="error">{translateMsg(error)}</p>}
            {preview && <ConfigDialog preview={preview} onClose={() => setPreview(null)} onDone={text => {
                setPreview(null)
                setDone(text)
                onApplied()
                window.dispatchEvent(new Event(CATALOG_CHANGED))
            }}/>}
        </section>
    )
}

function ConfigDialog({preview, onClose, onDone}: {
    preview: main.ConfigPreview
    onClose: () => void
    onDone: (text: string) => void
}) {
    const {t} = useTranslation()
    const s = preview.settings
    const [detection, setDetection] = useState(true)
    const [program, setProgram] = useState(true)
    const [machine, setMachine] = useState(false)
    const [roots, setRoots] = useState<Set<string>>(
        () => new Set(preview.roots.filter(r => r.exists && !r.added).map(r => r.path)))
    const [busy, setBusy] = useState(false)
    const [error, setError] = useState<Msg | null>(null)

    useEffect(() => {
        const onKey = (e: KeyboardEvent) => e.key === 'Escape' && !busy && onClose()
        window.addEventListener('keydown', onKey)
        return () => window.removeEventListener('keydown', onKey)
    }, [busy, onClose])

    const apply = async () => {
        setBusy(true)
        setError(null)
        try {
            const r = await ApplyConfig(main.ConfigApply.createFrom({detection, program, machine, roots: [...roots]}))
            let text = t('config.applied', {count: r.rootsAdded, n: formatNumber(r.rootsAdded)})
            if (r.skipped.length > 0) text += ' ' + t('config.skipped', {paths: r.skipped.join(', ')})
            onDone(text)
        } catch (e) {
            setError(toMsg(e))
        } finally {
            setBusy(false)
        }
    }
    const lang = s.language === '' ? t('config.langSystem') : s.language.toUpperCase()

    return (
        <div className="modal-backdrop" onMouseDown={e => e.target === e.currentTarget && !busy && onClose()}>
            <div className="modal modal-wide" role="dialog" aria-modal="true" aria-labelledby="config-title">
                <h2 id="config-title">{t('config.importTitle')}</h2>
                <p className="small muted"><PathText path={preview.file}/>
                    {preview.exported && <> · {formatDateTime(Date.parse(preview.exported))}</>}</p>

                <label className="row small">
                    <input type="checkbox" checked={detection} disabled={busy} onChange={e => setDetection(e.target.checked)}/>
                    <span>{t('config.detection', {
                        types: formatNumber(s.fileTypes.model.length + s.fileTypes.slicer.length + s.fileTypes.archive.length + s.fileTypes.supplementary.length),
                        parts: formatNumber(s.partNames.length), sizes: formatNumber(s.sizeTokens.length),
                    })}</span>
                </label>
                <label className="row small">
                    <input type="checkbox" checked={program} disabled={busy} onChange={e => setProgram(e.target.checked)}/>
                    <span>{t('config.program', {lang, hours: s.nasRescanHours})}</span>
                </label>
                {(s.slicerPath || s.externalBackupDir) && (
                    <label className="row small">
                        <input type="checkbox" checked={machine} disabled={busy} onChange={e => setMachine(e.target.checked)}/>
                        <span>{t('config.machine')}
                            {s.slicerPath && <><br/><code><PathText path={s.slicerPath}/></code></>}
                            {s.externalBackupDir && <><br/><code><PathText path={s.externalBackupDir}/></code></>}
                        </span>
                    </label>
                )}

                {preview.roots.length > 0 && <>
                    <h3>{t('config.rootsTitle')}</h3>
                    <ul className="plain">
                        {preview.roots.map(r => (
                            <li key={r.path}>
                                <label className="row small">
                                    <input type="checkbox" disabled={busy || r.added || !r.exists} checked={roots.has(r.path)}
                                           onChange={e => setRoots(prev => {
                                               const next = new Set(prev)
                                               if (e.target.checked) next.add(r.path)
                                               else next.delete(r.path)
                                               return next
                                           })}/>
                                    <code><PathText path={r.path}/></code>
                                    <span className="muted">
                                        {r.added ? t('config.rootAdded') : r.exists ? '' : t('config.rootMissing')}
                                    </span>
                                </label>
                            </li>
                        ))}
                    </ul>
                    <p className="small muted">{t('config.rootsHint')}</p>
                </>}
                <p className="small muted">{t('config.safeHint')}</p>
                {error && <p className="error">{translateMsg(error)}</p>}
                <div className="row modal-actions">
                    <button disabled={busy} onClick={onClose}>{t('confirm.cancel')}</button>
                    <button className="primary" disabled={busy || (!detection && !program && !machine && roots.size === 0)}
                            onClick={apply}>{busy ? t('import.importing') : t('config.apply')}</button>
                </div>
            </div>
        </div>
    )
}

/** Shown after the app folder was moved: root folders that moved along (M). */
export function MovedBanner({from, onDone}: { from: string; onDone: () => void }) {
    const {t} = useTranslation()
    const [list, setList] = useState<main.RootMove[] | null>(null)
    const [busy, setBusy] = useState(false)
    const [error, setError] = useState<Msg | null>(null)
    useEffect(() => {
        GetMoveSuggestions().then(setList).catch(e => setError(toMsg(e)))
    }, [from])

    const apply = async () => {
        if (!list) return
        setBusy(true)
        try {
            await ApplyMoveSuggestions(list.map(m => m.rootId))
            window.dispatchEvent(new Event(CATALOG_CHANGED))
            onDone()
        } catch (e) {
            setError(toMsg(e))
        } finally {
            setBusy(false)
        }
    }
    const dismiss = () => DismissMoved().then(onDone)

    return (
        <div className="notice-moved" role="status">
            <p className="small">{t('moved.text', {from})}</p>
            {list && list.length > 0 && <>
                <p className="small">{t('moved.roots', {count: list.length})}</p>
                <ul className="plain small">
                    {list.map(m => <li key={m.rootId}><code>{m.from}</code> → <code>{m.to}</code></li>)}
                </ul>
            </>}
            {error && <p className="error small">{translateMsg(error)}</p>}
            <div className="row">
                {list && list.length > 0 && (
                    <button className="primary small" disabled={busy} onClick={apply}>{t('moved.apply')}</button>
                )}
                <button className="small" disabled={busy} onClick={dismiss}>{t('moved.dismiss')}</button>
            </div>
        </div>
    )
}
