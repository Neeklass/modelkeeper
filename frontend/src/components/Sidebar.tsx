import {useEffect, useState} from 'react'
import {AppIcon} from './AppIcon'
import {useTranslation} from 'react-i18next'
import {CreateCollection, DeleteSavedSearch, GetScanProgress, PauseScan} from '../../wailsjs/go/main/App'
import {main, scanner} from '../../wailsjs/go/models'
import {EventsOn} from '../../wailsjs/runtime/runtime'
import {formatDuration, formatNumber} from '../format'
import {Library} from '../library'
import {emptyQuery, Query} from '../query'
import {useEta} from './RootsPanel'

export type View = 'library' | 'folders' | 'stats' | 'settings' | 'help'

export {libraryView, VIEW_QUERY} from '../query'
export type {LibraryView} from '../query'
import {libraryView, LibraryView, VIEW_QUERY} from '../query'

/** Navigation: library views, creators, tags, locations, and the app's state at the bottom. */
export function Sidebar({view, onView, query, onQuery, library, status}: {
    view: View
    onView: (v: View) => void
    query: Query
    onQuery: (q: Query) => void
    library: Library
    status: main.AppStatus
}) {
    const {t} = useTranslation()
    const current = view === 'library' && !query.collectionId ? libraryView(query) : null
    const [naming, setNaming] = useState(false)
    const [newName, setNewName] = useState('')
    const createCollection = async () => {
        const name = newName.trim()
        setNaming(false)
        setNewName('')
        if (!name) return
        try {
            const c = await CreateCollection(name)
            library.reload()
            onView('library')
            onQuery({...emptyQuery, collectionId: c.id})
        } catch {
            // the name was invalid; nothing created
        }
    }
    const pick = (lv: LibraryView) => onQuery({...emptyQuery, ...VIEW_QUERY[lv]})
    const backupProblem = status.lastRestoreTest && !status.lastRestoreTest.ok

    return (
        <nav className="sidebar" aria-label={t('nav.label')}>
            <div className="brand"><AppIcon/>{t('app.title')}</div>

            <div className="side-section">
                <SideItem icon="▦" label={t('nav.all')} count={library.counts.all}
                          active={current === 'all'} onClick={() => pick('all')}/>
                <SideItem icon="✎" label={t('nav.untagged')} count={library.counts.untagged} highlight
                          title={t('nav.untaggedHint')}
                          active={current === 'untagged'} onClick={() => pick('untagged')}/>
                <SideItem icon="★" label={t('nav.favorites')} count={library.counts.favorites}
                          active={current === 'favorites'} onClick={() => pick('favorites')}/>
                <SideItem icon="◷" label={t('nav.newest')}
                          active={current === 'newest'} onClick={() => pick('newest')}/>
                {library.counts.waiting > 0 && (
                    <SideItem icon="⧗" label={t('nav.waiting')} count={library.counts.waiting}
                              title={t('nav.waitingHint')}
                              active={current === 'waiting'} onClick={() => pick('waiting')}/>
                )}
            </div>

            <div className="side-section">
                <div className="side-title row spread">
                    <span>{t('nav.collections')}</span>
                    <button className="link small" onClick={() => setNaming(true)} title={t('collections.new')}
                            aria-label={t('collections.new')}>+</button>
                </div>
                {naming && (
                    <input className="side-filter" autoFocus value={newName} placeholder={t('collections.newPrompt')}
                           onChange={e => setNewName(e.target.value)} onBlur={createCollection}
                           onKeyDown={e => {
                               if (e.key === 'Enter') (e.target as HTMLInputElement).blur()
                               if (e.key === 'Escape') {
                                   setNewName('')
                                   setNaming(false)
                               }
                           }}/>
                )}
                {library.collections.map(c => (
                    <button key={c.id} className={`side-item sub${view === 'library' && query.collectionId === c.id ? ' active' : ''}`}
                            title={c.name} onClick={() => {
                                onView('library')
                                onQuery({...emptyQuery, collectionId: c.id})
                            }}>
                        <span className="ellipsis">{c.name}</span>
                        <span className="side-count">{formatNumber(c.models)}</span>
                    </button>
                ))}
                {library.collections.length === 0 && !naming && <p className="small muted side-hint">{t('collections.emptyHint')}</p>}
            </div>

            {library.savedSearches.length > 0 && (
                <div className="side-section">
                    <div className="side-title">{t('nav.savedSearches')}</div>
                    {library.savedSearches.map(s => (
                        <div key={s.id} className="side-item sub saved-search">
                            <button className="link ellipsis" title={s.name} onClick={() => {
                                onView('library')
                                try {
                                    onQuery({...emptyQuery, ...JSON.parse(s.query)})
                                } catch {
                                    onQuery(emptyQuery)
                                }
                            }}>{s.name}</button>
                            <button className="chip-x" aria-label={t('savedSearch.delete', {name: s.name})}
                                    title={t('savedSearch.delete', {name: s.name})}
                                    onClick={() => DeleteSavedSearch(s.id).then(library.reload)}>✕</button>
                        </div>
                    ))}
                </div>
            )}

            <SideList title={t('nav.creators')} items={library.creators.map(c => ({id: c.id, name: c.name, count: c.models}))}
                      active={view === 'library' ? query.creatorId : 0} limit={8}
                      onPick={id => onQuery({...query, creatorId: query.creatorId === id ? 0 : id})}/>
            {/* Tags without a group first, then one list per tag group (G1). */}
            <SideList title={t('nav.tags')} items={library.tags.filter(x => !x.group).map(x => ({id: x.id, name: x.name, count: x.models}))}
                      active={view === 'library' ? query.tagId : 0} limit={12} sortByCount
                      onPick={id => onQuery({...query, tagId: query.tagId === id ? 0 : id})}/>
            {[...new Set(library.tags.map(x => x.group).filter(Boolean))].sort((x, y) => x.localeCompare(y)).map(g => (
                <SideList key={g} title={g} items={library.tags.filter(x => x.group === g).map(x => ({id: x.id, name: x.name, count: x.models}))}
                          active={view === 'library' ? query.tagId : 0} limit={8} sortByCount
                          onPick={id => onQuery({...query, tagId: query.tagId === id ? 0 : id})}/>
            ))}

            {library.roots.length > 0 && (
                <div className="side-section">
                    <div className="side-title">{t('nav.locations')}</div>
                    {library.roots.map(r => (
                        <button key={r.id} className={`side-item${view === 'library' && query.rootId === r.id ? ' active' : ''}`}
                                title={r.path} onClick={() => onQuery({...query, rootId: query.rootId === r.id ? 0 : r.id})}>
                            <span className={`dot ${r.status}`} aria-hidden="true"/>
                            <span className="ellipsis">{rootName(r.path)}</span>
                            <span className="side-count">{t(`roots.kind.${r.kind}`)}</span>
                        </button>
                    ))}
                </div>
            )}

            <div className="side-footer">
                <ScanMini/>
                <SideItem icon="▤" label={t('tabs.stats')} active={view === 'stats'} onClick={() => onView('stats')}/>
                <SideItem icon="🗀" label={t('tabs.folders')} active={view === 'folders'} onClick={() => onView('folders')}/>
                <SideItem icon="⚙" label={t('tabs.settings')} active={view === 'settings'} onClick={() => onView('settings')}
                          warn={backupProblem} title={backupProblem ? t('backup.checkFailedShort') : undefined}/>
                <SideItem icon="?" label={t('tabs.help')} active={view === 'help'} onClick={() => onView('help')}/>
            </div>
        </nav>
    )
}

function SideItem({icon, label, count, active, onClick, highlight, warn, title}: {
    icon: string
    label: string
    count?: number
    active: boolean
    onClick: () => void
    highlight?: boolean
    warn?: boolean
    title?: string
}) {
    return (
        <button className={`side-item${active ? ' active' : ''}`} onClick={onClick} title={title}
                aria-current={active ? 'page' : undefined}>
            <span className="side-icon" aria-hidden="true">{icon}</span>
            <span className="ellipsis">{label}</span>
            {warn && <span className="side-warn" aria-hidden="true">!</span>}
            {count !== undefined && count > 0 &&
                <span className={`side-count${highlight ? ' highlight' : ''}`}>{formatNumber(count)}</span>}
        </button>
    )
}

/** A filterable list (creators, tags): the largest entries first, the rest on demand. */
function SideList({title, items, active, onPick, limit, sortByCount}: {
    title: string
    items: { id: number; name: string; count: number }[]
    active: number
    onPick: (id: number) => void
    limit: number
    sortByCount?: boolean
}) {
    const {t} = useTranslation()
    const [all, setAll] = useState(false)
    const [filter, setFilter] = useState('')
    if (items.length === 0) return null
    let shown = sortByCount ? [...items].sort((a, b) => b.count - a.count || a.name.localeCompare(b.name)) : items
    if (filter) shown = shown.filter(i => i.name.toLowerCase().includes(filter.toLowerCase()))
    else if (!all) {
        const top = shown.slice(0, limit)
        const sel = shown.find(i => i.id === active)
        shown = sel && !top.includes(sel) ? [...top, sel] : top
    }
    return (
        <div className="side-section">
            <div className="side-title">{title}</div>
            {all && items.length > limit && (
                <input className="side-filter" type="search" value={filter} autoFocus
                       placeholder={t('nav.filterList')} onChange={e => setFilter(e.target.value)}/>
            )}
            <div className={all ? 'side-scroll' : ''}>
                {shown.map(i => (
                    <button key={i.id} className={`side-item sub${i.id === active ? ' active' : ''}`}
                            onClick={() => onPick(i.id)} title={i.name}>
                        <span className="ellipsis">{i.name}</span>
                        <span className="side-count">{formatNumber(i.count)}</span>
                    </button>
                ))}
            </div>
            {items.length > limit && (
                <button className="link small side-more" onClick={() => {
                    setAll(a => !a)
                    setFilter('')
                }}>
                    {all ? t('nav.showLess') : t('nav.showAll', {n: formatNumber(items.length)})}
                </button>
            )}
        </div>
    )
}

function rootName(path: string): string {
    const parts = path.split(/[\\/]+/).filter(Boolean)
    return parts[parts.length - 1] ?? path
}

/** The running scan in a few words, with pause (F1). */
function ScanMini() {
    const {t} = useTranslation()
    const [p, setP] = useState<scanner.Progress | null>(null)
    useEffect(() => {
        GetScanProgress().then(x => x && setP(x)).catch(() => undefined)
        return EventsOn('scan:progress', (x: scanner.Progress) => setP(x))
    }, [])
    const eta = useEta(p ?? scanner.Progress.createFrom({}))
    if (!p || !p.running) return null
    const reading = p.phase === 'hashing' || p.phase === 'archives'
    const frac = reading && p.bytesTotal > 0 ? p.bytesDone / p.bytesTotal
        : p.phase === 'thumbnails' && p.thumbsTotal > 0 ? p.thumbsDone / p.thumbsTotal : null
    return (
        <div className="scan-mini" role="status">
            <div className="row spread small">
                <span className="ellipsis">{t(`scan.phase.${p.phase}`)}</span>
                {p.mode !== 'detect' && (
                    <button className="link small" onClick={() => PauseScan(p.rootId)} title={t('scan.pauseHint')}>
                        ❚❚ {t('scan.pause')}
                    </button>
                )}
            </div>
            {frac !== null && <div className="bar"><div style={{width: `${Math.min(100, frac * 100).toFixed(1)}%`}}/></div>}
            {eta !== null && <span className="small muted">{t('scan.eta', {time: formatDuration(eta)})}</span>}
        </div>
    )
}
