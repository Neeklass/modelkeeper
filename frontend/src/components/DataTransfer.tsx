import {useCallback, useEffect, useState} from 'react'
import {useTranslation} from 'react-i18next'
import {ApplyImport, CancelImport, ExportUserData, ListHistory, PickImportFile, PreviewImport} from '../../wailsjs/go/main/App'
import {catalog, main, userexport} from '../../wailsjs/go/models'
import {EventsOn} from '../../wailsjs/runtime/runtime'
import {Msg, toMsg, translateMsg} from '../i18n/msg'
import {formatDateTime, formatNumber} from '../format'
import {CATALOG_CHANGED, useToast} from './Toast'
import {PathText} from './PathText'

/**
 * Export and import of everything the user entered (J2, TODO 2.3): one
 * readable file that is enough to restore the cataloging work into an empty
 * catalog, on this or another PC. Every snapshot also holds such a file.
 */
export function DataTransferPanel() {
    const {t} = useTranslation()
    const [error, setError] = useState<Msg | null>(null)
    const [done, setDone] = useState<string | null>(null)
    const [busy, setBusy] = useState(false)
    const [preview, setPreview] = useState<main.ImportPreview | null>(null)

    const exportNow = async () => {
        setError(null)
        setDone(null)
        setBusy(true)
        try {
            const r = await ExportUserData()
            if (r) setDone(t('export.done', {
                path: r.path, models: formatNumber(r.models), tags: formatNumber(r.tags),
                overrides: formatNumber(r.overrides), thumbnails: formatNumber(r.thumbnails),
            }))
        } catch (e) {
            setError(toMsg(e))
        } finally {
            setBusy(false)
        }
    }

    const pick = async () => {
        setError(null)
        setDone(null)
        try {
            const p = await PickImportFile()
            if (p) setPreview(p)
        } catch (e) {
            setError(toMsg(e))
        }
    }

    return (
        <section className="card">
            <h2>{t('export.title')}</h2>
            <p className="small muted">{t('export.intro')}</p>
            <div className="row wrap">
                <button className="primary" disabled={busy} onClick={exportNow}>
                    {busy ? t('export.exporting') : t('export.button')}
                </button>
                <button disabled={busy} onClick={pick}>{t('import.button')}…</button>
            </div>
            {done && <p className="small ok-text">{done}</p>}
            <p className="small muted hint">{t('export.hint')}</p>
            {error && <p className="error">{translateMsg(error)}</p>}
            {preview && <ImportDialog initial={preview} onClose={() => {
                CancelImport()
                setPreview(null)
            }} onDone={text => {
                setPreview(null)
                setDone(text)
            }}/>}
        </section>
    )
}

function choiceValue(c: userexport.RootChoice): string {
    return c.action === 'existing' ? `existing:${c.rootId}` : c.action
}

/** Preview of an import: what the file holds, root folder mapping, then apply. */
function ImportDialog({initial, onClose, onDone}: {
    initial: main.ImportPreview
    onClose: () => void
    onDone: (text: string) => void
}) {
    const {t} = useTranslation()
    const toast = useToast()
    const [preview, setPreview] = useState(initial)
    const [choices, setChoices] = useState<userexport.RootChoice[]>(
        () => initial.plan.roots.map(r => userexport.RootChoice.createFrom(r.suggested)))
    const [importSettings, setImportSettings] = useState(false)
    const [busy, setBusy] = useState(false)
    const [error, setError] = useState<Msg | null>(null)
    const plan = preview.plan

    // Recompute the matching when a choice changes.
    useEffect(() => {
        let stale = false
        PreviewImport(choices).then(p => !stale && setPreview(p)).catch(e => !stale && setError(toMsg(e)))
        return () => {
            stale = true
        }
    }, [choices])

    useEffect(() => {
        const onKey = (e: KeyboardEvent) => {
            if (e.key === 'Escape' && !busy) onClose()
        }
        window.addEventListener('keydown', onKey)
        return () => window.removeEventListener('keydown', onKey)
    }, [busy, onClose])

    const setChoice = (key: string, value: string, path: string) => {
        setChoices(cs => cs.map(c => {
            if (c.key !== key) return c
            if (value.startsWith('existing:')) return userexport.RootChoice.createFrom({key, action: 'existing', rootId: Number(value.slice(9))})
            if (value === 'add') return userexport.RootChoice.createFrom({key, action: 'add', path})
            return userexport.RootChoice.createFrom({key, action: 'skip'})
        }))
    }

    const apply = async () => {
        setBusy(true)
        setError(null)
        try {
            const r = await ApplyImport(choices, importSettings)
            window.dispatchEvent(new Event(CATALOG_CHANGED))
            const text = t('import.done', {
                models: formatNumber(r.match.byId + r.match.byPath + r.match.byContent + r.match.new),
                waiting: formatNumber(r.match.new), tags: formatNumber(r.tags), overrides: formatNumber(r.overrides),
                thumbnails: formatNumber(r.thumbnails),
            })
            toast.show({text})
            onDone(text + ' ' + t('import.backupHint', {path: r.backup}))
        } catch (e) {
            setError(toMsg(e))
        } finally {
            setBusy(false)
        }
    }

    const m = plan.match
    return (
        <div className="modal-backdrop" onMouseDown={e => e.target === e.currentTarget && !busy && onClose()}>
            <div className="modal modal-wide" role="dialog" aria-modal="true" aria-labelledby="import-title">
                <h2 id="import-title">{t('import.title')}</h2>
                <p className="small muted"><PathText path={preview.file}/></p>
                <p className="small">
                    {t('import.contains', {
                        models: formatNumber(plan.models), tags: formatNumber(plan.tags),
                        overrides: formatNumber(plan.overrides), thumbnails: formatNumber(plan.thumbnails),
                    })}
                    {plan.exportedAt && <> {t('import.exportedAt', {when: formatDateTime(Date.parse(plan.exportedAt)), version: plan.appVersion || '?'})}</>}
                </p>

                {plan.roots.length > 0 && <>
                    <h3>{t('import.rootsTitle')}</h3>
                    <p className="small muted">{t('import.rootsHint')}</p>
                    <table className="import-roots">
                        <tbody>
                        {plan.roots.map(r => {
                            const c = choices.find(x => x.key === r.key)
                            return (
                                <tr key={r.key}>
                                    <td>
                                        <code><PathText path={r.path}/></code>
                                        <div className="small muted">
                                            {t('import.rootModels', {count: r.models, n: formatNumber(r.models)})}
                                            {r.removed && <> · {t('models.rootRemoved')}</>}
                                        </div>
                                    </td>
                                    <td>
                                        <select value={c ? choiceValue(c) : 'skip'} disabled={busy}
                                                onChange={e => setChoice(r.key, e.target.value, r.path)}>
                                            {preview.roots.map(l => (
                                                <option key={l.id} value={`existing:${l.id}`}>
                                                    {t('import.mapTo', {path: l.path})}{l.removedAt ? ` (${t('models.rootRemoved')})` : ''}
                                                </option>
                                            ))}
                                            {r.exists && <option value="add">{t('import.addRoot')}</option>}
                                            <option value="skip">{t('import.skipRoot')}</option>
                                        </select>
                                    </td>
                                </tr>
                            )
                        })}
                        </tbody>
                    </table>
                    <p className="small muted">{t('import.relocateHint')}</p>
                </>}

                <h3>{t('import.matchTitle')}</h3>
                <ul className="plain small">
                    <li>{t('import.byId', {n: formatNumber(m.byId)})}</li>
                    <li>{t('import.byPath', {n: formatNumber(m.byPath)})}</li>
                    <li>{t('import.byContent', {n: formatNumber(m.byContent)})}</li>
                    <li>{t('import.new', {n: formatNumber(m.new)})}</li>
                    {m.notesKept > 0 && <li className="warn-text">{t('import.notesKept', {count: m.notesKept, n: formatNumber(m.notesKept)})}</li>}
                </ul>
                <p className="small muted">{t('import.safeHint')}</p>
                {plan.hasSettings && (
                    <label className="row small">
                        <input type="checkbox" checked={importSettings} disabled={busy}
                               onChange={e => setImportSettings(e.target.checked)}/>
                        {t('import.settings')}
                    </label>
                )}
                {error && <p className="error">{translateMsg(error)}</p>}
                <div className="row modal-actions">
                    <button disabled={busy} onClick={onClose}>{t('confirm.cancel')}</button>
                    <button className="primary" disabled={busy || plan.models === 0} onClick={apply}>
                        {busy ? t('import.importing') : t('import.apply')}
                    </button>
                </div>
            </div>
        </div>
    )
}

/** The newest changes of user data, the newest one can be undone (TODO 2.1). */
export function HistoryPanel() {
    const {t} = useTranslation()
    const toast = useToast()
    const [list, setList] = useState<catalog.HistoryEntry[]>([])
    const [open, setOpen] = useState(false)
    const load = useCallback(() => {
        ListHistory().then(setList).catch(() => setList([]))
    }, [])
    useEffect(() => {
        if (!open) return
        load()
        window.addEventListener(CATALOG_CHANGED, load)
        const off = EventsOn('scan:progress', (p: { running: boolean }) => !p.running && load())
        return () => {
            window.removeEventListener(CATALOG_CHANGED, load)
            off()
        }
    }, [open, load])
    const firstOpen = list.findIndex(h => !h.undone)

    return (
        <section className="card">
            <h2>{t('history.title')}</h2>
            <p className="small muted">{t('history.intro')}</p>
            <details onToggle={e => setOpen((e.target as HTMLDetailsElement).open)}>
                <summary>{t('history.show')}</summary>
                {list.length === 0 && <p className="small muted">{t('history.empty')}</p>}
                <ul className="plain history">
                    {list.map((h, i) => (
                        <li key={h.id} className={`row spread small${h.undone ? ' muted' : ''}`}>
                            <span>
                                <span className="muted nowrap">{formatDateTime(h.at)}</span>{' '}
                                {t(h.action, {...(h.params ?? {}), defaultValue: h.action})}
                                {h.undone && <> · {t('history.wasUndone')}</>}
                            </span>
                            {i === firstOpen && (
                                <button className="link small" onClick={() => toast.undo().then(load)}>{t('history.undo')}</button>
                            )}
                        </li>
                    ))}
                </ul>
            </details>
        </section>
    )
}
