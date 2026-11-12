import {Fragment, ReactNode, useMemo, useState} from 'react'
import {useTranslation} from 'react-i18next'

/** A section of the user manual (translation files, key "manual.sections"). */
interface Section {
    id: string
    title: string
    body: string[]
}

/** Renders **bold** spans (names of buttons and fields) in a manual line. */
function inline(text: string): ReactNode {
    return text.split(/\*\*(.+?)\*\*/g).map((part, i) =>
        i % 2 === 1 ? <strong key={i}>{part}</strong> : <Fragment key={i}>{part}</Fragment>)
}

/** Paragraphs; consecutive lines starting with "- " form a list, "1. " a numbered list. */
function Body({lines}: { lines: string[] }) {
    const blocks: ReactNode[] = []
    let list: { ordered: boolean; items: string[] } | null = null
    const flush = () => {
        if (!list) return
        const items = list.items.map((it, i) => <li key={i}>{inline(it)}</li>)
        blocks.push(list.ordered ? <ol key={blocks.length}>{items}</ol> : <ul key={blocks.length}>{items}</ul>)
        list = null
    }
    for (const line of lines) {
        const bullet = line.match(/^- (.*)$/s)
        const numbered = line.match(/^\d+\. (.*)$/s)
        if (bullet || numbered) {
            const ordered = !!numbered
            if (list && list.ordered !== ordered) flush()
            if (!list) list = {ordered, items: []}
            list.items.push((bullet ?? numbered)![1])
            continue
        }
        flush()
        blocks.push(<p key={blocks.length}>{inline(line)}</p>)
    }
    flush()
    return <>{blocks}</>
}

/** The user manual, in the UI language (falls back to English). */
export function HelpView() {
    const {t} = useTranslation()
    const [search, setSearch] = useState('')
    const sections = t('manual.sections', {returnObjects: true}) as unknown as Section[]
    const list = Array.isArray(sections) ? sections : []

    const shown = useMemo(() => {
        const words = search.toLowerCase().split(/\s+/).filter(Boolean)
        if (!words.length) return list
        return list.filter(s => {
            const text = (s.title + ' ' + s.body.join(' ')).toLowerCase()
            return words.every(w => text.includes(w))
        })
    }, [list, search])

    const jump = (id: string) => document.getElementById(`help-${id}`)?.scrollIntoView({behavior: 'smooth', block: 'start'})

    return (
        <div className="help">
            <nav className="help-toc" aria-label={t('manual.contents')}>
                <input type="search" className="help-search" placeholder={t('manual.search')} value={search}
                       onChange={e => setSearch(e.target.value)}/>
                <h3>{t('manual.contents')}</h3>
                <ul className="plain">
                    {shown.map(s => (
                        <li key={s.id}><button className="link" onClick={() => jump(s.id)}>{s.title}</button></li>
                    ))}
                </ul>
            </nav>
            <article className="help-content">
                <h1>{t('manual.title')}</h1>
                <p className="help-intro">{t('manual.intro')}</p>
                {shown.length === 0 && <p className="muted">{t('manual.noResults')}</p>}
                {shown.map(s => (
                    <section key={s.id} id={`help-${s.id}`} className="help-section">
                        <h2>{s.title}</h2>
                        <Body lines={s.body}/>
                    </section>
                ))}
            </article>
        </div>
    )
}
