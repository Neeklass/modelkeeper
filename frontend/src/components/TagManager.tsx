import {useCallback, useEffect, useRef, useState} from 'react'
import {useTranslation} from 'react-i18next'
import {DeleteTag, ListTags, RenameTag} from '../../wailsjs/go/main/App'
import {catalog} from '../../wailsjs/go/models'
import {Msg, toMsg, translateMsg} from '../i18n/msg'
import {formatNumber} from '../format'
import {CATALOG_CHANGED, useToast} from './Toast'
import {useConfirm} from './ConfirmDialog'

/**
 * All tags with their counts; rename (a name that exists already merges the
 * two, e.g. to fix a typo) and delete for all models at once (TODO 2.2).
 * Every change is undoable.
 */
export function TagManagerPanel() {
    const {t} = useTranslation()
    const toast = useToast()
    const confirm = useConfirm()
    const [tags, setTags] = useState<catalog.TagCount[]>([])
    const [filter, setFilter] = useState('')
    const [editing, setEditing] = useState<string | null>(null)
    const [value, setValue] = useState('')
    const typed = useRef('') // what rename reads (blur can come after Escape)
    const [busy, setBusy] = useState(false)
    const [error, setError] = useState<Msg | null>(null)
    const load = useCallback(() => {
        ListTags().then(setTags).catch(() => setTags([]))
    }, [])
    useEffect(() => {
        load()
        window.addEventListener(CATALOG_CHANGED, load)
        return () => window.removeEventListener(CATALOG_CHANGED, load)
    }, [load])

    const run = async (fn: () => Promise<string>) => {
        setBusy(true)
        setError(null)
        try {
            const text = await fn()
            window.dispatchEvent(new Event(CATALOG_CHANGED))
            toast.show({text, undo: true})
        } catch (e) {
            setError(toMsg(e))
        } finally {
            setBusy(false)
        }
    }

    const rename = (from: string) => {
        const to = typed.current.trim()
        typed.current = ''
        setEditing(null)
        if (!to || to === from) return
        const existing = tags.find(x => x.name.toLowerCase() === to.toLowerCase() && x.name !== from)
        run(async () => {
            if (existing && !await confirm({
                message: t('tagManager.confirmMerge', {from, to: existing.name, n: formatNumber(existing.models)}),
                confirmLabel: t('tagManager.merge'),
            })) throw new Error(JSON.stringify({key: 'tagManager.cancelled'}))
            const n = await RenameTag(from, to)
            return existing ? t('tagManager.merged', {from, to: existing.name, count: n})
                : t('tagManager.renamed', {from, to, count: n})
        })
    }

    const remove = (tag: catalog.TagCount) => run(async () => {
        if (!await confirm({
            message: t('tagManager.confirmDelete', {name: tag.name, count: tag.models, n: formatNumber(tag.models)}),
            confirmLabel: t('tagManager.delete'), danger: true,
        })) throw new Error(JSON.stringify({key: 'tagManager.cancelled'}))
        const n = await DeleteTag(tag.name)
        return t('tagManager.deleted', {name: tag.name, count: n})
    })

    const f = filter.trim().toLowerCase()
    const shown = tags.filter(x => !f || x.name.toLowerCase().includes(f))
    return (
        <section className="card">
            <h2>{t('tagManager.title')}</h2>
            <p className="small muted">{t('tagManager.intro')}</p>
            <input type="search" placeholder={t('tagManager.filter')} value={filter} onChange={e => setFilter(e.target.value)}/>
            {tags.length === 0 && <p className="small muted">{t('tags.none')}</p>}
            <ul className="plain tag-manager">
                {shown.slice(0, 300).map(x => (
                    <li key={x.id} className="row spread">
                        {editing === x.name ? (
                            <input autoFocus value={value} disabled={busy} onChange={e => {
                                       typed.current = e.target.value
                                       setValue(e.target.value)
                                   }}
                                   onBlur={() => rename(x.name)}
                                   onKeyDown={e => {
                                       if (e.key === 'Enter') (e.target as HTMLInputElement).blur()
                                       if (e.key === 'Escape') {
                                           typed.current = '' // blur then renames nothing
                                           setEditing(null)
                                       }
                                   }}/>
                        ) : (
                            <span className="row">
                                <span className="chip tag">{x.name}</span>
                                <span className="small muted">{t('models.count', {count: x.models, n: formatNumber(x.models)})}</span>
                            </span>
                        )}
                        <span className="row nowrap">
                            <button className="link small" disabled={busy} onClick={() => {
                                setEditing(x.name)
                                setValue(x.name)
                                typed.current = x.name
                            }}>{t('tagManager.rename')}</button>
                            <button className="link small danger-text" disabled={busy} onClick={() => remove(x)}>
                                {t('tagManager.delete')}
                            </button>
                        </span>
                    </li>
                ))}
            </ul>
            {shown.length > 300 && <p className="small muted">{t('tagManager.more', {n: formatNumber(shown.length - 300)})}</p>}
            {error && error.key !== 'tagManager.cancelled' && <p className="error">{translateMsg(error)}</p>}
        </section>
    )
}
