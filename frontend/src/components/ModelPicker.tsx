import {useEffect, useRef, useState} from 'react'
import {useTranslation} from 'react-i18next'
import {SearchModels} from '../../wailsjs/go/main/App'
import {catalog} from '../../wailsjs/go/models'
import {Thumb} from './Thumb'

const LIMIT = 40

/**
 * Picks a target model by search: for "add to another model" and for moving
 * single files (F4d). The model itself (exclude) is not offered.
 */
export function ModelPicker({title, intro, exclude, onPick, onClose}: {
    title: string
    intro?: string
    exclude: number
    onPick: (id: number, name: string) => void
    onClose: () => void
}) {
    const {t} = useTranslation()
    const [text, setText] = useState('')
    const [rows, setRows] = useState<catalog.ModelRow[]>([])
    const [total, setTotal] = useState(0)
    const [active, setActive] = useState(0)
    const inputRef = useRef<HTMLInputElement>(null)

    useEffect(() => inputRef.current?.focus(), [])
    useEffect(() => {
        let stale = false
        const timer = setTimeout(() => {
            SearchModels(catalog.ModelQuery.createFrom({text: text.trim()}), 0, LIMIT + 1).then(page => {
                if (stale) return
                const items = (page.items ?? []).filter(r => r.id !== exclude).slice(0, LIMIT)
                setRows(items)
                setTotal(Math.max(0, page.total - (page.items?.some(r => r.id === exclude) ? 1 : 0)))
                setActive(0)
            }).catch(() => !stale && setRows([]))
        }, 150)
        return () => {
            stale = true
            clearTimeout(timer)
        }
    }, [text, exclude])

    const onKey = (e: React.KeyboardEvent) => {
        if (e.key === 'Escape') onClose()
        else if (e.key === 'ArrowDown') {
            e.preventDefault()
            setActive(a => Math.min(a + 1, rows.length - 1))
        } else if (e.key === 'ArrowUp') {
            e.preventDefault()
            setActive(a => Math.max(a - 1, 0))
        } else if (e.key === 'Enter' && rows[active]) {
            e.preventDefault()
            onPick(rows[active].id, rows[active].name)
        }
    }

    return (
        <div className="modal-backdrop" onMouseDown={e => e.target === e.currentTarget && onClose()}>
            <div className="modal picker" role="dialog" aria-modal="true" aria-labelledby="picker-title" onKeyDown={onKey}>
                <h2 id="picker-title">{title}</h2>
                {intro && <p className="small muted">{intro}</p>}
                <input ref={inputRef} type="search" value={text} placeholder={t('picker.search')}
                       onChange={e => setText(e.target.value)}/>
                <ul className="plain picker-list" role="listbox">
                    {rows.map((r, i) => (
                        <li key={r.id} role="option" aria-selected={i === active}
                            className={i === active ? 'active' : ''}
                            onMouseEnter={() => setActive(i)} onClick={() => onPick(r.id, r.name)}>
                            <Thumb src={r.thumb} className="row-thumb" label={r.name}/>
                            <span className="picker-text">
                                <span className="ellipsis">{r.name}</span>
                                <span className="small muted ellipsis">
                                    {[r.creator, t('picker.files', {count: r.files})].filter(Boolean).join(' · ')}
                                </span>
                            </span>
                        </li>
                    ))}
                    {rows.length === 0 && <li className="muted small">{t('picker.none')}</li>}
                </ul>
                {total > rows.length && <p className="small muted">{t('picker.more', {count: total - rows.length})}</p>}
                <div className="row modal-actions">
                    <button onClick={onClose}>{t('confirm.cancel')}</button>
                </div>
            </div>
        </div>
    )
}
