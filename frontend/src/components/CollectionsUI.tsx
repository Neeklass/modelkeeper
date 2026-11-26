import {useEffect, useState} from 'react'
import {useTranslation} from 'react-i18next'
import {
    AddToCollection, CreateCollection, DeleteCollection, MoveInCollection, RemoveFromCollection, RenameCollection,
    SaveSearch, UpdateModels,
} from '../../wailsjs/go/main/App'
import {catalog} from '../../wailsjs/go/models'
import {Msg, toMsg, translateMsg} from '../i18n/msg'
import {emptyQuery, Query} from '../query'
import {useConfirm} from './ConfirmDialog'

/** Name, rename and delete of the collection the library shows (G6). */
export function CollectionHeader({collection, onChanged, onDeleted}: {
    collection: catalog.Collection
    onChanged: () => void
    onDeleted: () => void
}) {
    const {t} = useTranslation()
    const confirm = useConfirm()
    const [editing, setEditing] = useState(false)
    const [name, setName] = useState(collection.name)
    const [error, setError] = useState<Msg | null>(null)
    useEffect(() => setName(collection.name), [collection.name])
    const save = async () => {
        setEditing(false)
        if (name.trim() === collection.name || !name.trim()) return setName(collection.name)
        try {
            await RenameCollection(collection.id, name.trim())
            onChanged()
        } catch (e) {
            setError(toMsg(e))
            setName(collection.name)
        }
    }
    return (
        <span className="row">
            {editing ? (
                <input className="name-input" autoFocus value={name} onChange={e => setName(e.target.value)} onBlur={save}
                       onKeyDown={e => {
                           if (e.key === 'Enter') (e.target as HTMLInputElement).blur()
                           if (e.key === 'Escape') {
                               setName(collection.name)
                               setEditing(false)
                           }
                       }}/>
            ) : <h1>{collection.name}</h1>}
            {!editing && <>
                <button className="link small" onClick={() => setEditing(true)} aria-label={t('collections.rename')}
                        title={t('collections.rename')}>✎</button>
                <button className="link small danger-text" title={t('collections.delete')} onClick={async () => {
                    if (!await confirm({message: t('collections.confirmDelete', {name: collection.name}), danger: true})) return
                    try {
                        await DeleteCollection(collection.id)
                        onDeleted()
                    } catch (e) {
                        setError(toMsg(e))
                    }
                }}>{t('collections.delete')}</button>
            </>}
            {error && <span className="error small">{translateMsg(error)}</span>}
        </span>
    )
}

/** Saves the current search and filters under a name (shown in the sidebar). */
export function SaveSearchButton({query, onSaved}: { query: Query; onSaved: () => void }) {
    const {t} = useTranslation()
    const [naming, setNaming] = useState(false)
    const [name, setName] = useState('')
    const [done, setDone] = useState(false)
    // Only what differs from an empty query is stored.
    const diff: Record<string, unknown> = {}
    for (const [k, v] of Object.entries(query)) {
        if (v !== (emptyQuery as unknown as Record<string, unknown>)[k]) diff[k] = v
    }
    if (Object.keys(diff).length === 0) return null
    const save = async () => {
        setNaming(false)
        const n = name.trim()
        setName('')
        if (!n) return
        try {
            await SaveSearch(n, JSON.stringify(diff))
            setDone(true)
            window.setTimeout(() => setDone(false), 2500)
            onSaved()
        } catch {
            // invalid name: nothing saved
        }
    }
    if (naming) {
        return (
            <input className="small" autoFocus value={name} placeholder={t('savedSearch.namePlaceholder')}
                   onChange={e => setName(e.target.value)} onBlur={save}
                   onKeyDown={e => {
                       if (e.key === 'Enter') (e.target as HTMLInputElement).blur()
                       if (e.key === 'Escape') {
                           setName('')
                           setNaming(false)
                       }
                   }}/>
        )
    }
    return (
        <button className="subtle small" onClick={() => setNaming(true)} title={t('savedSearch.saveHint')}>
            {done ? t('savedSearch.saved') : `☆ ${t('savedSearch.save')}`}
        </button>
    )
}

/** Selection: add to a collection (or a new one); in a collection: remove, move. */
export function CollectionActions({ids, collections, current, onChanged, disabled}: {
    ids: number[]
    collections: catalog.Collection[]
    current: number
    onChanged: (text?: string) => void
    disabled: boolean
}) {
    const {t} = useTranslation()
    const [choice, setChoice] = useState('')
    const [newName, setNewName] = useState('')
    const [error, setError] = useState<Msg | null>(null)
    const run = async (fn: () => Promise<string | undefined>) => {
        setError(null)
        try {
            onChanged(await fn())
        } catch (e) {
            setError(toMsg(e))
        }
    }
    const add = () => run(async () => {
        let id = Number(choice)
        let name = collections.find(c => c.id === id)?.name ?? ''
        if (choice === 'new') {
            const c = await CreateCollection(newName)
            id = c.id
            name = c.name
        }
        if (!id) return undefined
        const n = await AddToCollection(id, ids)
        setChoice('')
        setNewName('')
        return t('collections.added', {count: n, name})
    })
    return (
        <div className="row wrap">
            <label className="small">
                {t('collections.addTo')}
                <select value={choice} disabled={disabled} onChange={e => setChoice(e.target.value)}>
                    <option value="">–</option>
                    {collections.filter(c => c.id !== current).map(c => <option key={c.id} value={c.id}>{c.name}</option>)}
                    <option value="new">{t('collections.newOption')}</option>
                </select>
            </label>
            {choice === 'new' && <input className="small" value={newName} placeholder={t('collections.newPrompt')}
                                        onChange={e => setNewName(e.target.value)}/>}
            {choice && <button disabled={disabled || (choice === 'new' && !newName.trim())} onClick={add}>{t('collections.addButton')}</button>}
            {current > 0 && <>
                <button disabled={disabled} onClick={() => run(async () => {
                    await RemoveFromCollection(current, ids)
                    return t('collections.removed', {count: ids.length})
                })}>{t('collections.removeSelected')}</button>
                {ids.length === 1 && <>
                    <button disabled={disabled} aria-label={t('collections.moveUp')} title={t('collections.moveUp')}
                            onClick={() => run(async () => {
                                await MoveInCollection(current, ids[0], -1)
                                return undefined
                            })}>↑</button>
                    <button disabled={disabled} aria-label={t('collections.moveDown')} title={t('collections.moveDown')}
                            onClick={() => run(async () => {
                                await MoveInCollection(current, ids[0], 1)
                                return undefined
                            })}>↓</button>
                </>}
            </>}
            {error && <span className="error small">{translateMsg(error)}</span>}
        </div>
    )
}

const KEEP = ''

/** The same fields for all selected models (one undoable action). */
export function EditFieldsDialog({ids, onClose, onDone}: { ids: number[]; onClose: () => void; onDone: (text: string) => void }) {
    const {t} = useTranslation()
    const [printStatus, setPrintStatus] = useState(KEEP)
    const [supports, setSupports] = useState(KEEP)
    const [text, setText] = useState<Record<string, string | null>>({creator: null, origin: null, originUrl: null, license: null})
    const [busy, setBusy] = useState(false)
    const [error, setError] = useState<Msg | null>(null)
    useEffect(() => {
        const onKey = (e: KeyboardEvent) => e.key === 'Escape' && !busy && onClose()
        window.addEventListener('keydown', onKey)
        return () => window.removeEventListener('keydown', onKey)
    }, [busy, onClose])
    const apply = async () => {
        const u: Record<string, string> = {}
        if (printStatus !== KEEP) u.printStatus = printStatus
        if (supports !== KEEP) u.supports = supports
        for (const [k, v] of Object.entries(text)) if (v !== null) u[k] = v
        if (Object.keys(u).length === 0) return onClose()
        setBusy(true)
        try {
            await UpdateModels(ids, catalog.ModelUpdate.createFrom(u))
            onDone(t('bulk.done', {count: ids.length}))
        } catch (e) {
            setError(toMsg(e))
        } finally {
            setBusy(false)
        }
    }
    const field = (key: string, label: string) => (
        <label className="row small">
            <input type="checkbox" checked={text[key] !== null} disabled={busy}
                   onChange={e => setText(s => ({...s, [key]: e.target.checked ? '' : null}))}/>
            <span className="bulk-label">{label}</span>
            <input value={text[key] ?? ''} disabled={busy || text[key] === null}
                   placeholder={text[key] === null ? t('bulk.keep') : t('bulk.emptyClears')}
                   onChange={e => setText(s => ({...s, [key]: e.target.value}))}/>
        </label>
    )
    return (
        <div className="modal-backdrop" onMouseDown={e => e.target === e.currentTarget && !busy && onClose()}>
            <div className="modal" role="dialog" aria-modal="true" aria-labelledby="bulk-title">
                <h2 id="bulk-title">{t('bulk.title', {count: ids.length})}</h2>
                <p className="small muted">{t('bulk.intro')}</p>
                <label className="row small">
                    <span className="bulk-label">{t('models.printStatus')}</span>
                    <select value={printStatus} disabled={busy} onChange={e => setPrintStatus(e.target.value)}>
                        <option value={KEEP}>{t('bulk.keep')}</option>
                        {['not_printed', 'printed', 'failed'].map(s => <option key={s} value={s}>{t(`printStatus.${s}`)}</option>)}
                    </select>
                </label>
                <label className="row small">
                    <span className="bulk-label">{t('models.supportsLabel')}</span>
                    <select value={supports} disabled={busy} onChange={e => setSupports(e.target.value)}>
                        <option value={KEEP}>{t('bulk.keep')}</option>
                        {['auto', 'presupported', 'unsupported', 'both', 'none'].map(s => <option key={s} value={s}>{t(`supports.${s}`)}</option>)}
                    </select>
                </label>
                {field('creator', t('models.creator'))}
                {field('origin', t('models.origin'))}
                {field('originUrl', t('models.originUrl'))}
                {field('license', t('models.license'))}
                {error && <p className="error">{translateMsg(error)}</p>}
                <div className="row modal-actions">
                    <button disabled={busy} onClick={onClose}>{t('confirm.cancel')}</button>
                    <button className="primary" disabled={busy} onClick={apply}>{t('bulk.apply')}</button>
                </div>
            </div>
        </div>
    )
}
