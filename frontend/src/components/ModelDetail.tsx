import {useEffect, useState} from 'react'
import {useTranslation} from 'react-i18next'
import {
    AddTags, DetachAnchor, DismissSuggestion, GetModel, ListThumbnailCandidates, MarkFolderModel, MarkFolderNotModel,
    OpenInSlicer, PickThumbnailFile, RemoveTag, ResetCorrections, ResetThumbnail, SetThumbnail, SetThumbnailFromView,
    ShowInExplorer, SplitModel, UpdateModel,
} from '../../wailsjs/go/main/App'
import {catalog, thumbs} from '../../wailsjs/go/models'
import {Thumb} from './Thumb'
import {Msg, toMsg, translateMsg} from '../i18n/msg'
import {formatBytes, formatNumber} from '../format'
import {TagInput} from './TagInput'
import {FavoriteStar} from './FavoriteStar'
import {canPreview, defaultPreviewFile, Preview3D} from './Preview3D'
import {useConfirm} from './ConfirmDialog'
import {PathText} from './PathText'
import {Lightbox, LightboxImage} from './Lightbox'

const PRINT_STATUSES = ['not_printed', 'printed', 'failed']
const SUPPORTS = ['presupported', 'unsupported', 'both', 'none']
/** Picture types shown in the gallery (internal/preview.ImageExts). */
const IMAGE_EXTS = new Set(['jpg', 'jpeg', 'png', 'webp', 'gif'])
/** Picture types the backend can make a thumbnail of. */
const THUMBABLE = new Set(['jpg', 'jpeg', 'png'])
const MAX_GALLERY = 40

/** What the large picture area shows. */
type Media = { kind: 'thumb' } | { kind: 'image'; file: catalog.ModelFile } | { kind: '3d'; locationId: number }

/** Pictures of a model: present image files, each content once (D4). */
function galleryImages(files: catalog.ModelFile[]): catalog.ModelFile[] {
    const seen = new Set<string>()
    const out: catalog.ModelFile[] = []
    for (const f of files) {
        if (!f.present || !IMAGE_EXTS.has(f.ext)) continue
        const key = f.hash || `loc${f.locationId}`
        if (seen.has(key)) continue
        seen.add(key)
        out.push(f)
        if (out.length >= MAX_GALLERY) break
    }
    return out
}

/** Files a slicer can open: models and slicer projects, the likely one first (H5). */
function slicerFiles(files: catalog.ModelFile[]): catalog.ModelFile[] {
    const list = files.filter(f => f.present && (f.category === 'model' || f.category === 'slicer'))
    const best = list.find(f => f.category === 'slicer') ?? defaultPreviewFile(list) ?? list[0]
    return best ? [best, ...list.filter(f => f !== best)] : []
}

/**
 * Detail view of a model (H4) with editing (L1, L2, G1, F5), pictures,
 * "Open in slicer" (H5) and corrections (F4d). In the cataloging view the
 * tag field has the focus, so tagging one model after the other needs no mouse.
 */
export function ModelDetailPanel({id, version, allTags, onChanged, onClose, onPrev, onNext, position, cataloging, onShowCreator}: {
    id: number
    version: number
    allTags: catalog.TagCount[]
    onChanged: () => void
    onClose: () => void
    onPrev?: () => void
    onNext?: () => void
    /** Place of the model in the list, for "12 / 340". */
    position?: { index: number; total: number }
    cataloging?: boolean
    onShowCreator?: (name: string) => void
}) {
    const {t} = useTranslation()
    const [d, setD] = useState<catalog.ModelDetail | null>(null)
    const [error, setError] = useState<Msg | null>(null)
    const [busy, setBusy] = useState(false)
    const [media, setMedia] = useState<Media>({kind: 'thumb'})
    const [lightbox, setLightbox] = useState<number | null>(null)
    const [opened, setOpened] = useState<string | null>(null)

    useEffect(() => {
        setMedia({kind: 'thumb'})
        setLightbox(null)
        setOpened(null)
    }, [id])
    useEffect(() => {
        setError(null)
        GetModel(id).then(setD).catch(e => {
            const m = toMsg(e)
            if (m.key === 'error.modelNotFound') onClose() // merged or split away
            else setError(m)
        })
    }, [id, version]) // eslint-disable-line react-hooks/exhaustive-deps

    const confirm = useConfirm()
    const act = async (fn: () => Promise<void>, confirmText?: string) => {
        if (confirmText && !await confirm({message: confirmText})) return
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

    if (!d || d.id !== id) {
        return <aside className="detail">
            <DetailNav onPrev={onPrev} onNext={onNext} onClose={onClose} position={position}/>
            {error ? <p className="error">{translateMsg(error)}</p> : <div className="detail-skeleton"/>}
        </aside>
    }

    const images = galleryImages(d.files)
    const slicer = slicerFiles(d.files)
    const preview3d = defaultPreviewFile(d.files)
    const boxImages: LightboxImage[] = [
        ...(d.thumb ? [{src: d.thumb, label: t('thumbs.current')}] : []),
        ...images.map(f => ({src: `/files/${f.locationId}`, label: basename(f.path)})),
    ]
    const openSlicer = (f: catalog.ModelFile) => {
        setError(null)
        setOpened(null)
        OpenInSlicer(f.locationId).then(() => setOpened(basename(f.path))).catch(e => setError(toMsg(e)))
    }

    const groups = new Map<string, catalog.ModelFile[]>()
    for (const f of d.files) {
        const key = f.variant || ''
        groups.set(key, [...(groups.get(key) ?? []), f])
    }

    return (
        <aside className="detail" key={d.id} aria-label={d.name}>
            <DetailNav onPrev={onPrev} onNext={onNext} onClose={onClose} position={position}/>

            <div className="detail-head">
                <div className="row detail-title">
                    <EditableName name={d.name} disabled={busy} onSave={name => update({name})}/>
                    <FavoriteStar id={d.id} on={d.favorite} onChanged={onChanged} className="big"/>
                </div>
                <div className="row small muted">
                    {d.creator
                        ? <button className="link" onClick={() => onShowCreator?.(d.creator)}
                                  title={t('models.showCreator', {name: d.creator})}>{d.creator}</button>
                        : <span>{t('models.noCreator')}</span>}
                    {d.variants.length > 0 && <span>· {d.variants.join(', ')}</span>}
                    {d.inactive && <span className="warn-text">· {t('models.inactive')}</span>}
                </div>
            </div>
            {error && <p className="error">{translateMsg(error)}</p>}

            <div className="media">
                {media.kind === '3d' ? (
                    <Preview3D files={d.files} locationId={media.locationId}
                               onSelect={loc => setMedia({kind: '3d', locationId: loc})} busy={busy}
                               onUseAsThumbnail={url => act(() => SetThumbnailFromView(d.id, url))}/>
                ) : media.kind === 'image' ? (
                    <button className="media-main" onClick={() => setLightbox(boxImages.findIndex(b => b.src === `/files/${media.file.locationId}`))}
                            title={t('gallery.enlarge')}>
                        <img src={`/files/${media.file.locationId}`} alt={basename(media.file.path)}/>
                    </button>
                ) : (
                    <button className="media-main" onClick={() => d.thumb && setLightbox(0)} disabled={!d.thumb}
                            title={d.thumb ? t('gallery.enlarge') : undefined}>
                        <Thumb src={d.thumb} className="detail-thumb" label={d.name}/>
                    </button>
                )}
                {(images.length > 0 || preview3d) && (
                    <div className="filmstrip" role="list">
                        <button className={`film${media.kind === 'thumb' ? ' active' : ''}`} role="listitem"
                                onClick={() => setMedia({kind: 'thumb'})} title={t('thumbs.current')}>
                            <Thumb src={d.thumb} className="film-img" label={d.name}/>
                        </button>
                        {preview3d && (
                            <button className={`film film-3d${media.kind === '3d' ? ' active' : ''}`} role="listitem"
                                    onClick={() => setMedia({kind: '3d', locationId: preview3d.locationId})} title={t('preview.show')}>
                                3D
                            </button>
                        )}
                        {images.map(f => (
                            <button key={f.locationId} role="listitem" title={basename(f.path)}
                                    className={`film${media.kind === 'image' && media.file.locationId === f.locationId ? ' active' : ''}`}
                                    onClick={() => setMedia({kind: 'image', file: f})}>
                                <img className="film-img" src={`/files/${f.locationId}`} alt="" loading="lazy"/>
                            </button>
                        ))}
                    </div>
                )}
                <ThumbnailOptions d={d} busy={busy} act={act}
                                  image={media.kind === 'image' && THUMBABLE.has(media.file.ext) ? media.file : null}/>
            </div>

            <div className="actions">
                {slicer.length > 0 && (
                    <SlicerButton files={slicer} onOpen={openSlicer}/>
                )}
                {(slicer[0] ?? d.files.find(f => f.present)) && (
                    <button onClick={() => ShowInExplorer((slicer[0] ?? d.files.find(f => f.present))!.locationId).catch(e => setError(toMsg(e)))}>
                        {t('models.showInExplorer')}
                    </button>
                )}
            </div>
            {opened && <p className="small ok-text">{t('slicer.opened', {name: opened})}</p>}

            <section className="detail-section">
                <h3>{t('tags.title')}</h3>
                <div className="row wrap tags">
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
                        {d.tagSuggestions.length > 1 && (
                            <button className="link small" disabled={busy}
                                    onClick={() => act(() => AddTags([d.id], d.tagSuggestions))}>{t('tags.acceptAll')}</button>
                        )}
                    </div>
                )}
                <TagInput key={d.id} allTags={allTags} placeholder={t('tags.addPlaceholder')} buttonLabel={t('tags.add')}
                          disabled={busy} autoFocus={cataloging} onSubmit={names => act(() => AddTags([d.id], names))}/>
                {cataloging && (
                    <p className="small muted">{t('catalogMode.detailHint')}
                        {onNext && <> <button className="link small" onClick={onNext}>{t('catalogMode.skip')} ›</button></>}
                    </p>
                )}
            </section>

            <section className="detail-section">
                <dl className="props">
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
                </dl>
            </section>

            <section className="detail-section">
                <h3>{t('models.notes')}</h3>
                <Notes notes={d.notes} disabled={busy} onSave={notes => update({notes})}/>
            </section>

            <details className="detail-section">
                <summary><h3>{t('models.filesTitle', {n: d.files.length})}</h3></summary>
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
                                        <span className="row nowrap">
                                            {canPreview(f) && (
                                                <button className={`link small${media.kind === '3d' && media.locationId === f.locationId ? ' active' : ''}`}
                                                        title={t('preview.show')} onClick={() => setMedia({kind: '3d', locationId: f.locationId})}>
                                                    3D
                                                </button>
                                            )}
                                            {f.present && (f.category === 'model' || f.category === 'slicer') && (
                                                <button className="link small" title={t('slicer.openHint')} onClick={() => openSlicer(f)}>
                                                    {t('slicer.openShort')}
                                                </button>
                                            )}
                                            <button className="link small" onClick={() => ShowInExplorer(f.locationId).catch(e => setError(toMsg(e)))}>
                                                {t('models.showInExplorerShort')}
                                            </button>
                                        </span>
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
            </details>

            <details className="detail-section">
                <summary><h3>{t('models.locations')} <span className="muted">({formatNumber(d.anchors.length)})</span></h3></summary>
                {d.inactive && <p className="small notice-inactive">{t('models.inactiveNotice')}</p>}
                <ul className="plain small anchors">
                    {d.anchors.map(a => (
                        <li key={a.rootId + a.path}>
                            <span className={`badge ${a.rootRemoved ? 'status-warn' : `status-${a.online ? 'online' : 'offline'}`}`}
                                  title={a.rootRemoved ? t('models.inactiveHint') : undefined}>
                                {t(`roots.kind.${a.rootKind}`)}{a.rootRemoved && <> · {t('models.rootRemoved')}</>}
                            </span>
                            <code className="anchor-path">
                                <span className="muted"><PathText path={a.rootPath + '\\'}/></span>
                                <span className="anchor-rel"><PathText path={a.path.replaceAll('/', '\\')}/></span>
                            </code>
                        </li>
                    ))}
                </ul>
            </details>

            <Corrections d={d} busy={busy} act={act}/>

            {lightbox !== null && lightbox >= 0 && boxImages.length > 0 && (
                <Lightbox images={boxImages} index={Math.min(lightbox, boxImages.length - 1)}
                          onIndex={setLightbox} onClose={() => setLightbox(null)}/>
            )}
        </aside>
    )
}

/** Previous / next model and close, at the top of the detail panel. */
function DetailNav({onPrev, onNext, onClose, position}: {
    onPrev?: () => void
    onNext?: () => void
    onClose: () => void
    position?: { index: number; total: number }
}) {
    const {t} = useTranslation()
    return (
        <div className="row spread detail-nav">
            <span className="row">
                <button className="icon" onClick={onPrev} disabled={!onPrev} aria-label={t('models.prev')} title={`${t('models.prev')} (←)`}>‹</button>
                <button className="icon" onClick={onNext} disabled={!onNext} aria-label={t('models.next')} title={`${t('models.next')} (→)`}>›</button>
                {position && <span className="small muted">{t('models.position', {i: formatNumber(position.index + 1), n: formatNumber(position.total)})}</span>}
            </span>
            <button className="icon" onClick={onClose} aria-label={t('models.close')} title={`${t('models.close')} (Esc)`}>✕</button>
        </div>
    )
}

/** "Open in slicer" with the likely file, and a menu for the others (H5). */
function SlicerButton({files, onOpen}: { files: catalog.ModelFile[]; onOpen: (f: catalog.ModelFile) => void }) {
    const {t} = useTranslation()
    const [open, setOpen] = useState(false)
    return (
        <span className="split-button">
            <button className="primary" onClick={() => onOpen(files[0])} title={t('slicer.openHint')}>
                ▶ {t('slicer.open')}
            </button>
            {files.length > 1 && (
                <button className="primary split-toggle" aria-expanded={open} aria-label={t('slicer.chooseFile')}
                        onClick={() => setOpen(o => !o)}>▾</button>
            )}
            {open && (
                <ul className="menu" role="menu" onMouseLeave={() => setOpen(false)}>
                    {files.map(f => (
                        <li key={f.locationId} role="none">
                            <button role="menuitem" onClick={() => {
                                setOpen(false)
                                onOpen(f)
                            }}>
                                <span className="ellipsis">{f.variant ? `${f.variant} · ` : ''}{basename(f.path)}</span>
                                <span className="muted small">{f.ext.toUpperCase()} · {formatBytes(f.size)}</span>
                            </button>
                        </li>
                    ))}
                </ul>
            )}
        </span>
    )
}

/** Changing the thumbnail (I4): candidates, a file, the picture shown, automatic. */
function ThumbnailOptions({d, busy, act, image}: {
    d: catalog.ModelDetail
    busy: boolean
    act: (fn: () => Promise<void>, confirmText?: string) => void
    image: catalog.ModelFile | null
}) {
    const {t} = useTranslation()
    const [open, setOpen] = useState(false)
    const [cands, setCands] = useState<thumbs.Candidate[] | null>(null)
    useEffect(() => {
        if (open) ListThumbnailCandidates(d.id).then(setCands).catch(() => setCands([]))
    }, [open, d.id])

    return (
        <div className="thumb-options-row">
            <div className="row small muted spread">
                <span>{d.thumbSource ? t(`thumbs.source.${d.thumbSource}`) : t('thumbs.none')}</span>
                <span className="row nowrap">
                    {image && (
                        <button className="link small" disabled={busy}
                                onClick={() => act(() => SetThumbnail(d.id, image.locationId))}>{t('gallery.useAsThumbnail')}</button>
                    )}
                    <button className="link small" onClick={() => setOpen(o => !o)}>{t('thumbs.change')}</button>
                </span>
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
                    <p className="small muted">{t('thumbs.fromViewHint')}</p>
                </div>
            )}
        </div>
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
