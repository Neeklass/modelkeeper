import {useCallback, useEffect, useRef, useState} from 'react'
import {useTranslation} from 'react-i18next'
import {AddTagAlias, DeleteTag, FindSimilarTags, ListTags, RemoveTagAlias, RenameTag, SetTagGroup} from '../../wailsjs/go/main/App'
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
    const [similar, setSimilar] = useState<catalog.SimilarTags[]>([])
    const [aliasFor, setAliasFor] = useState<string | null>(null)
    const [aliasText, setAliasText] = useState('')
    const load = useCallback(() => {
        ListTags().then(setTags).catch(() => setTags([]))
        FindSimilarTags().then(setSimilar).catch(() => setSimilar([]))
    }, [])
    const groups = [...new Set(tags.map(x => x.group).filter(Boolean))].sort((a, b) => a.localeCompare(b))
    // Changes that are not in the undo history (aliases, groups).
    const quiet = async (fn: () => Promise<unknown>) => {
        setError(null)
        try {
            await fn()
            load()
            window.dispatchEvent(new Event(CATALOG_CHANGED))
        } catch (e) {
            setError(toMsg(e))
        }
    }
    const addAlias = (tag: string) => {
        const alias = aliasText.trim()
        setAliasFor(null)
        setAliasText('')
        if (!alias) return
        const used = tags.find(x => x.name.toLowerCase() === alias.toLowerCase())
        run(async () => {
            if (used && !await confirm({
                message: t('tagManager.confirmAliasMerge', {alias: used.name, tag, count: used.models, n: formatNumber(used.models)}),
                confirmLabel: t('tagManager.merge'),
            })) throw new Error(JSON.stringify({key: 'tagManager.cancelled'}))
            const n = await AddTagAlias(alias, tag)
            load()
            return t('tagManager.aliasAdded', {alias, tag, count: n})
        }, !!used) // only a merge is in the history
    }
    const mergeSimilar = (g: catalog.SimilarTags) => {
        const [keep, ...rest] = g.tags
        run(async () => {
            if (!await confirm({
                message: t('tagManager.confirmMergeSimilar', {tags: rest.map(x => x.name).join(', '), to: keep.name}),
                confirmLabel: t('tagManager.merge'),
            })) throw new Error(JSON.stringify({key: 'tagManager.cancelled'}))
            let n = 0
            for (const x of rest) n += await AddTagAlias(x.name, keep.name)
            load()
            return t('tagManager.mergedSimilar', {to: keep.name, count: n})
        }, rest.length === 1) // several merges: undo them one by one in the history
    }
    useEffect(() => {
        load()
        window.addEventListener(CATALOG_CHANGED, load)
        return () => window.removeEventListener(CATALOG_CHANGED, load)
    }, [load])

    const run = async (fn: () => Promise<string>, undo = true) => {
        setBusy(true)
        setError(null)
        try {
            const text = await fn()
            window.dispatchEvent(new Event(CATALOG_CHANGED))
            toast.show({text, undo})
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
            {similar.length > 0 && (
                <div className="notice-inactive">
                    <p className="small">{t('tagManager.similarIntro')}</p>
                    <ul className="plain small">
                        {similar.slice(0, 20).map(g => (
                            <li key={g.tags.map(x => x.id).join('-')} className="row spread">
                                <span>{g.tags.map(x => <span key={x.id} className="chip tag">{x.name} ({formatNumber(x.models)})</span>)}</span>
                                <button className="link small" disabled={busy} onClick={() => mergeSimilar(g)}
                                        title={t('tagManager.mergeSimilarHint', {to: g.tags[0].name})}>
                                    {t('tagManager.mergeInto', {to: g.tags[0].name})}
                                </button>
                            </li>
                        ))}
                    </ul>
                </div>
            )}
            <datalist id="tag-groups">{groups.map(g => <option key={g} value={g}/>)}</datalist>
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
                            <span className="row wrap">
                                <span className="chip tag">{x.name}</span>
                                <span className="small muted">{t('models.count', {count: x.models, n: formatNumber(x.models)})}</span>
                                {x.aliases?.map(a => (
                                    <span key={a} className="chip alias small" title={t('tagManager.aliasHint', {alias: a, tag: x.name})}>
                                        = {a}
                                        <button className="chip-x" aria-label={t('tagManager.removeAlias', {alias: a})}
                                                onClick={() => quiet(() => RemoveTagAlias(a))}>✕</button>
                                    </span>
                                ))}
                                {aliasFor === x.name
                                    ? <input className="small" autoFocus value={aliasText} placeholder={t('tagManager.aliasPlaceholder')}
                                             onChange={e => setAliasText(e.target.value)} onBlur={() => addAlias(x.name)}
                                             onKeyDown={e => {
                                                 if (e.key === 'Enter') (e.target as HTMLInputElement).blur()
                                                 if (e.key === 'Escape') {
                                                     setAliasText('')
                                                     setAliasFor(null)
                                                 }
                                             }}/>
                                    : <button className="link small" disabled={busy} onClick={() => setAliasFor(x.name)}
                                              title={t('tagManager.addAliasHint')}>+ {t('tagManager.alias')}</button>}
                            </span>
                        )}
                        <span className="row nowrap">
                            <input className="small group-input" list="tag-groups" defaultValue={x.group}
                                   key={`${x.name}:${x.group}`} placeholder={t('tagManager.group')}
                                   aria-label={t('tagManager.groupOf', {tag: x.name})}
                                   onBlur={e => {
                                       const g = e.target.value.trim()
                                       if (g !== (x.group ?? '')) quiet(() => SetTagGroup(x.name, g))
                                   }}
                                   onKeyDown={e => e.key === 'Enter' && (e.target as HTMLInputElement).blur()}/>
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
