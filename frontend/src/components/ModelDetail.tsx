import {useEffect, useState} from 'react'
import {useTranslation} from 'react-i18next'
import {
    AddTags, DetachAnchor, DismissSuggestion, GetModel, ListThumbnailCandidates, MarkFolderModel, MarkFolderNotModel,
    PickThumbnailFile, RemoveTag, ResetCorrections, ResetThumbnail, SetThumbnail, ShowInExplorer, SplitModel,
    UpdateModel,
} from '../../wailsjs/go/main/App'
import {catalog, thumbs} from '../../wailsjs/go/models'
import {Thumb} from './Thumb'
import {Msg, toMsg, translateMsg} from '../i18n/msg'
import {formatBytes} from '../format'
import {TagInput} from './TagInput'

const PRINT_STATUSES = ['not_printed', 'printed', 'failed']
const SUPPORTS = ['presupported', 'unsupported', 'both', 'none']

/** Detail view of a model (H4) with editing (L1, L2, G1, F5) and corrections (F4d). */
export function ModelDetailPanel({id, version, allTags, onChanged, onClose}: {
    id: number
    version: number
    allTags: catalog.TagCount[]
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
    const update = (u: Partial<catalog.ModelUpdate>) => act(() => UpdateModel(id, catalog.ModelUpdate.createFrom(u)))

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
        <aside className="detail" key={d.id}>
            <div className="row spread">
                <EditableName name={d.name} disabled={busy} onSave={name => update({name})}/>
                <button onClick={onClose} aria-label={t('models.close')}>✕</button>
            </div>
            {error && <p className="error">{translateMsg(error)}</p>}

            <ThumbnailSection d={d} busy={busy} act={act}/>

            <dl className="props">
                <dt>{t('models.creator')}</dt><dd>{d.creator || '—'}</dd>
                <dt>{t('models.printStatus')}</dt>
                <dd>
                    <select value={d.printStatus} disabled={busy} onChange={e => update({printStatus: e.target.value})}>
                        {PRINT_STATUSES.map(s => <option key={s} value={s}>{t(`printStatus.${s}`)}</option>)}
                    </select>
                </dd>
                <dt>{t('models.supportsLabel')}</dt>
                <dd>
                    <select value={d.supportsManual ? (d.supports || 'none') : 'auto'} disabled={busy}
                            onChange={e => update({supports: e.target.value})}>
                        <option value="auto">
                            {d.supportsManual ? t('supports.auto')
                                : t('supports.autoWith', {value: t(`supports.${d.supports || 'none'}`)})}
                        </option>
                        {SUPPORTS.map(s => <option key={s} value={s}>{t(`supports.${s}`)}</option>)}
                    </select>
                </dd>
                {d.variants.length > 0 && <><dt>{t('models.variants')}</dt><dd>{d.variants.join(', ')}</dd></>}
            </dl>

            <h3>{t('tags.title')}</h3>
            <div className="row wrap">
                {d.tags.map(tag => (
                    <span key={tag} className="chip tag">
                        {tag}
                        <button className="chip-x" aria-label={t('tags.remove', {name: tag})} disabled={busy}
                                onClick={() => act(() => RemoveTag([d.id], tag))}>✕</button>
                    </span>
                ))}
                {d.tags.length === 0 && <span className="muted small">{t('tags.none')}</span>}
            </div>
            {d.tagSuggestions.length > 0 && (
                <div className="row wrap suggestions">
                    <span className="small muted">{t('models.tagSuggestions')}:</span>
                    {d.tagSuggestions.map(s => (
                        <span key={s} className="chip suggestion">
                            <button className="link" disabled={busy} title={t('tags.accept')}
                                    onClick={() => act(() => AddTags([d.id], [s]))}>+ {s}</button>
                            <button className="chip-x" disabled={busy} aria-label={t('tags.dismiss', {name: s})}
                                    onClick={() => act(() => DismissSuggestion(d.id, s))}>✕</button>
                        </span>
                    ))}
                </div>
            )}
            <TagInput allTags={allTags} placeholder={t('tags.addPlaceholder')} buttonLabel={t('tags.add')}
                      disabled={busy} onSubmit={names => act(() => AddTags([d.id], names))}/>

            <h3>{t('models.notes')}</h3>
            <Notes notes={d.notes} disabled={busy} onSave={notes => update({notes})}/>

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

function EditableName({name, disabled, onSave}: { name: string; disabled: boolean; onSave: (n: string) => void }) {
    const {t} = useTranslation()
    const [editing, setEditing] = useState(false)
    const [value, setValue] = useState(name)
    useEffect(() => setValue(name), [name])
    if (!editing) {
        return (
            <h2 className="row">
                <span>{name}</span>
                <button className="link small" onClick={() => setEditing(true)} aria-label={t('models.rename')}
                        title={t('models.rename')}>✎</button>
            </h2>
        )
    }
    const save = () => {
        setEditing(false)
        if (value.trim() && value.trim() !== name) onSave(value.trim())
        else setValue(name)
    }
    return (
        <input className="name-input" autoFocus value={value} disabled={disabled}
               onChange={e => setValue(e.target.value)} onBlur={save}
               onKeyDown={e => {
                   if (e.key === 'Enter') save()
                   if (e.key === 'Escape') {
                       setValue(name)
                       setEditing(false)
                   }
               }}/>
    )
}

function Notes({notes, disabled, onSave}: { notes: string; disabled: boolean; onSave: (n: string) => void }) {
    const {t} = useTranslation()
    const [value, setValue] = useState(notes)
    useEffect(() => setValue(notes), [notes])
    return (
        <textarea className="notes" rows={3} value={value} disabled={disabled} placeholder={t('models.notesPlaceholder')}
                  onChange={e => setValue(e.target.value)}
                  onBlur={() => value !== notes && onSave(value)}/>
    )
}

/** Thumbnail with the options to change it (I4). */
function ThumbnailSection({d, busy, act}: {
    d: catalog.ModelDetail
    busy: boolean
    act: (fn: () => Promise<void>, confirmText?: string) => void
}) {
    const {t} = useTranslation()
    const [open, setOpen] = useState(false)
    const [cands, setCands] = useState<thumbs.Candidate[] | null>(null)
    useEffect(() => {
        if (open) ListThumbnailCandidates(d.id).then(setCands).catch(() => setCands([]))
    }, [open, d.id])

    return (
        <div className="thumb-section">
            <Thumb src={d.thumb} className="detail-thumb"/>
            <div className="row small muted spread">
                <span>{d.thumbSource ? t(`thumbs.source.${d.thumbSource}`) : t('thumbs.none')}</span>
                <button className="link small" onClick={() => setOpen(o => !o)}>{t('thumbs.change')}</button>
            </div>
            {open && (
                <div className="thumb-options">
                    {cands === null && <span className="muted small">{t('app.loading')}</span>}
                    {cands?.length === 0 && <span className="muted small">{t('thumbs.noCandidates')}</span>}
                    <ul className="plain small">
                        {cands?.map(c => (
                            <li key={c.locationId} className="row spread">
                                <span className="ellipsis" title={c.label}>{c.label}
                                    <span className="muted"> · {t(`thumbs.source.${c.kind}`)}</span></span>
                                <button className="link" disabled={busy}
                                        onClick={() => act(() => SetThumbnail(d.id, c.locationId))}>{t('thumbs.use')}</button>
                            </li>
                        ))}
                    </ul>
                    <div className="row wrap">
                        <button disabled={busy} onClick={() => act(async () => {
                            await PickThumbnailFile(d.id)
                        })}>{t('thumbs.pickFile')}</button>
                        {d.thumbSource === 'manual' && (
                            <button disabled={busy} onClick={() => act(() => ResetThumbnail(d.id))}>
                                {t('thumbs.automatic')}
                            </button>
                        )}
                    </div>
                </div>
            )}
        </div>
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
