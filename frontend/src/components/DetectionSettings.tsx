import {useEffect, useState} from 'react'
import {useTranslation} from 'react-i18next'
import {GetDetectionSettings, SaveDetectionRules, SaveFileTypes} from '../../wailsjs/go/main/App'
import {main, settings} from '../../wailsjs/go/models'
import {Msg, toMsg, translateMsg} from '../i18n/msg'
import {formatNumber} from '../format'
import {useConfirm} from './ConfirmDialog'

const CATEGORIES = ['model', 'slicer', 'archive', 'supplementary'] as const
type Category = typeof CATEGORIES[number]

type Lists = Record<Category, string[]>

function toLists(ft: settings.FileTypes): Lists {
    return {model: ft.model ?? [], slicer: ft.slicer ?? [], archive: ft.archive ?? [], supplementary: ft.supplementary ?? []}
}

const same = (a: string[], b: string[]) => a.length === b.length && a.every((x, i) => x === b[i])

/** Editable file types, part names and size tokens (F10). */
export function DetectionSettingsPanel() {
    const [view, setView] = useState<main.DetectionSettingsView | null>(null)
    const [error, setError] = useState<Msg | null>(null)

    const load = () => GetDetectionSettings().then(setView).catch(e => setError(toMsg(e)))
    useEffect(() => {
        load()
    }, [])

    if (!view) return error ? <section className="card"><p className="error">{translateMsg(error)}</p></section> : null
    return (
        <>
            <FileTypesCard current={toLists(view.current.fileTypes)} defaults={toLists(view.defaults.fileTypes)} onSaved={load}/>
            <RulesCard parts={view.current.partNames ?? []} sizes={view.current.sizeTokens ?? []}
                       defaults={view.defaults} onSaved={load}/>
        </>
    )
}

function FileTypesCard({current, defaults, onSaved}: { current: Lists; defaults: Lists; onSaved: () => void }) {
    const {t} = useTranslation()
    const [lists, setLists] = useState<Lists>(current)
    const [busy, setBusy] = useState(false)
    const [error, setError] = useState<Msg | null>(null)
    const [result, setResult] = useState<string | null>(null)
    useEffect(() => setLists(current), [JSON.stringify(current)]) // eslint-disable-line react-hooks/exhaustive-deps

    const dirty = CATEGORIES.some(c => !same(lists[c], current[c]))
    const isDefault = CATEGORIES.every(c => same(lists[c], defaults[c]))
    const all = (l: Lists) => new Set(CATEGORIES.flatMap(c => l[c]))
    const before = all(current), after = all(lists)
    const removed = [...before].filter(e => !after.has(e))
    const added = [...after].filter(e => !before.has(e))

    const confirm = useConfirm()
    const save = async () => {
        const parts: string[] = []
        if (removed.length) parts.push(t('settings.types.confirmRemoved', {list: removed.join(', ')}))
        if (added.length) parts.push(t('settings.types.confirmAdded'))
        if (parts.length && !await confirm({message: [t('settings.types.confirmTitle'), ...parts].join('\n\n'), confirmLabel: t('settings.save'), danger: removed.length > 0})) return
        setBusy(true)
        setError(null)
        setResult(null)
        try {
            const ch = await SaveFileTypes(settings.FileTypes.createFrom(lists))
            const msgs = [t('settings.saved')]
            if (ch.removed) msgs.push(t('settings.types.resultRemoved', {count: ch.removed, n: formatNumber(ch.removed)}))
            if (ch.archivesPending) msgs.push(t('settings.types.resultArchives', {count: ch.archivesPending, n: formatNumber(ch.archivesPending)}))
            if (added.length) msgs.push(t('settings.types.resultScan'))
            setResult(msgs.join(' '))
            onSaved()
        } catch (e) {
            setError(toMsg(e))
        } finally {
            setBusy(false)
        }
    }

    return (
        <section className="card settings-lists">
            <h2>{t('settings.types.title')}</h2>
            <p className="small muted">{t('settings.types.hint')}</p>
            {CATEGORIES.map(c => (
                <ListEditor key={c} label={t(`settings.types.${c}`)} items={lists[c]} disabled={busy} prefix="." split={/[,;\s]+/}
                            placeholder={t('settings.types.addPlaceholder')}
                            normalize={s => s.trim().replace(/^\./, '').toLowerCase()}
                            onChange={items => setLists({...lists, [c]: items})}/>
            ))}
            <Actions dirty={dirty} isDefault={isDefault} busy={busy} onSave={save}
                     onDiscard={() => setLists(current)} onDefaults={() => setLists(defaults)}/>
            {result && <p className="small">{result}</p>}
            {error && <p className="error">{translateMsg(error)}</p>}
        </section>
    )
}

function RulesCard({parts, sizes, defaults, onSaved}: {
    parts: string[]
    sizes: string[]
    defaults: main.DetectionSettings
    onSaved: () => void
}) {
    const {t} = useTranslation()
    const [p, setP] = useState(parts)
    const [s, setS] = useState(sizes)
    const [busy, setBusy] = useState(false)
    const [error, setError] = useState<Msg | null>(null)
    const [result, setResult] = useState<string | null>(null)
    useEffect(() => setP(parts), [parts.join('\n')]) // eslint-disable-line react-hooks/exhaustive-deps
    useEffect(() => setS(sizes), [sizes.join('\n')]) // eslint-disable-line react-hooks/exhaustive-deps

    const dp = defaults.partNames ?? [], ds = defaults.sizeTokens ?? []
    const dirty = !same(p, parts) || !same(s, sizes)
    const isDefault = same(p, dp) && same(s, ds)

    const save = async () => {
        setBusy(true)
        setError(null)
        setResult(null)
        try {
            await SaveDetectionRules(p, s)
            setResult(`${t('settings.saved')} ${t('settings.rules.resultRedetect')}`)
            onSaved()
        } catch (e) {
            setError(toMsg(e))
        } finally {
            setBusy(false)
        }
    }

    return (
        <section className="card settings-lists">
            <h2>{t('settings.rules.title')}</h2>
            <ListEditor label={t('settings.rules.partNames')} hint={t('settings.rules.partNamesHint')} items={p}
                        disabled={busy} placeholder={t('settings.rules.addPlaceholder')} onChange={setP}/>
            <ListEditor label={t('settings.rules.sizeTokens')} hint={t('settings.rules.sizeTokensHint')} items={s}
                        disabled={busy} placeholder={t('settings.rules.addPlaceholder')} onChange={setS}/>
            <Actions dirty={dirty} isDefault={isDefault} busy={busy} onSave={save}
                     onDiscard={() => {
                         setP(parts)
                         setS(sizes)
                     }}
                     onDefaults={() => {
                         setP(dp)
                         setS(ds)
                     }}/>
            {result && <p className="small">{result}</p>}
            {error && <p className="error">{translateMsg(error)}</p>}
        </section>
    )
}

function Actions({dirty, isDefault, busy, onSave, onDiscard, onDefaults}: {
    dirty: boolean
    isDefault: boolean
    busy: boolean
    onSave: () => void
    onDiscard: () => void
    onDefaults: () => void
}) {
    const {t} = useTranslation()
    return (
        <div className="row wrap">
            <button className="primary" disabled={!dirty || busy} onClick={onSave}>{t('settings.save')}</button>
            <button disabled={!dirty || busy} onClick={onDiscard}>{t('settings.discard')}</button>
            <button className="link small" disabled={isDefault || busy} onClick={onDefaults}>{t('settings.defaults')}</button>
        </div>
    )
}

/** A list of short entries shown as chips; Enter or comma adds, ✕ removes. */
function ListEditor({label, hint, items, disabled, placeholder, prefix = '', split = /[,;]+/, normalize = s => s.trim(), onChange}: {
    label: string
    hint?: string
    items: string[]
    disabled: boolean
    placeholder: string
    prefix?: string
    split?: RegExp // separators when several entries are typed at once
    normalize?: (s: string) => string
    onChange: (items: string[]) => void
}) {
    const {t} = useTranslation()
    const [value, setValue] = useState('')
    const add = () => {
        const next = [...items]
        for (const part of value.split(split)) {
            const v = normalize(part)
            if (v && !next.some(x => x.toLowerCase() === v.toLowerCase())) next.push(v)
        }
        onChange(next)
        setValue('')
    }
    return (
        <div className="list-editor">
            <div className="small"><strong>{label}</strong>{hint && <span className="muted"> · {hint}</span>}</div>
            <div className="row wrap">
                {items.map(it => (
                    <span key={it} className="chip tag">
                        {prefix}{it}
                        <button className="chip-x" disabled={disabled} aria-label={t('settings.removeEntry', {name: it})}
                                onClick={() => onChange(items.filter(x => x !== it))}>✕</button>
                    </span>
                ))}
                <input className="list-add" value={value} disabled={disabled} placeholder={placeholder}
                       onChange={e => setValue(e.target.value)}
                       onBlur={() => value.trim() && add()}
                       onKeyDown={e => {
                           if (e.key === 'Enter' || e.key === ',') {
                               e.preventDefault()
                               add()
                           }
                       }}/>
            </div>
        </div>
    )
}
