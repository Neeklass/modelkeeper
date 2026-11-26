import {useEffect, useState} from 'react'
import {useTranslation} from 'react-i18next'
import {GetStats} from '../../wailsjs/go/main/App'
import {catalog} from '../../wailsjs/go/models'
import {formatBytes, formatNumber} from '../format'
import {Msg, toMsg, translateMsg} from '../i18n/msg'
import {Query} from '../query'

/**
 * Storage statistics: how much is stored in which folder, by which creator,
 * in which format, which models are the largest, and how much is stored more
 * than once (same fingerprint in several places). Read-only.
 */
export function StatsView({onShowModels}: { onShowModels: (q: Partial<Query>) => void }) {
    const {t} = useTranslation()
    const [st, setSt] = useState<catalog.Stats | null>(null)
    const [error, setError] = useState<Msg | null>(null)
    const load = () => {
        setError(null)
        GetStats().then(setSt).catch(e => setError(toMsg(e)))
    }
    useEffect(load, [])

    if (error) return <main className="page"><h1>{t('tabs.stats')}</h1><p className="error">{translateMsg(error)}</p></main>
    if (!st) return <main className="page"><h1>{t('tabs.stats')}</h1><p className="muted">{t('stats.loading')}</p></main>

    return (
        <main className="page stats">
            <div className="row spread">
                <h1>{t('tabs.stats')}</h1>
                <button onClick={load}>{t('stats.refresh')}</button>
            </div>
            <section className="card stat-tiles">
                <Tile label={t('stats.total')} value={formatBytes(st.bytes)} sub={t('stats.files', {n: formatNumber(st.files)})}/>
                <Tile label={t('stats.duplicates')} value={formatBytes(st.dupExtra)}
                      sub={t('stats.dupFiles', {n: formatNumber(st.dupFiles)})}/>
                <Tile label={t('stats.share')} value={st.bytes > 0 ? `${Math.round(st.dupExtra / st.bytes * 1000) / 10} %` : '–'}
                      sub={t('stats.shareHint')}/>
            </section>
            {st.unhashed > 0 && <p className="small muted">{t('stats.unhashed', {n: formatNumber(st.unhashed)})}</p>}

            <Bars title={t('stats.byRoot')} rows={st.roots} total={st.bytes}
                  onPick={r => r.id ? onShowModels({rootId: r.id}) : undefined}/>
            <Bars title={t('stats.byCreator')} rows={st.creators.map(c => ({...c, key: c.key || t('models.noCreator')}))}
                  total={st.bytes} onPick={r => r.id ? onShowModels({creatorId: r.id}) : undefined}/>
            <div className="stats-two">
                <Bars title={t('stats.byCategory')} total={st.bytes}
                      rows={st.categories.map(c => ({...c, key: t(`stats.category.${c.key}`, {defaultValue: c.key})}))}/>
                <Bars title={t('stats.byExt')} rows={st.exts.map(c => ({...c, key: '.' + c.key}))} total={st.bytes}
                      onPick={r => onShowModels({ext: r.key.slice(1)})}/>
            </div>

            <section className="card">
                <h2>{t('stats.largest')}</h2>
                <table className="stats-table">
                    <tbody>
                    {st.models.map(m => (
                        <tr key={m.id}>
                            <td className="ellipsis">{m.name}</td>
                            <td className="num">{formatBytes(m.bytes)}</td>
                        </tr>
                    ))}
                    </tbody>
                </table>
            </section>

            <section className="card">
                <h2>{t('stats.dupTitle')}</h2>
                <p className="small muted">{t('stats.dupHint')}</p>
                {st.duplicates.length === 0 ? <p className="small muted">{t('stats.noDuplicates')}</p> : (
                    <table className="stats-table">
                        <thead>
                        <tr>
                            <th>{t('stats.file')}</th>
                            <th className="num">{t('stats.copies')}</th>
                            <th className="num">{t('stats.size')}</th>
                            <th className="num">{t('stats.extra')}</th>
                        </tr>
                        </thead>
                        <tbody>
                        {st.duplicates.map(d => (
                            <tr key={d.hash}>
                                <td className="ellipsis" title={d.path}><code>{d.path}</code></td>
                                <td className="num">{formatNumber(d.copies)}</td>
                                <td className="num">{formatBytes(d.size)}</td>
                                <td className="num">{formatBytes(d.extra)}</td>
                            </tr>
                        ))}
                        </tbody>
                    </table>
                )}
                {st.dupFiles > 0 && (
                    <button className="link small" onClick={() => onShowModels({duplicates: true})}>{t('stats.showDuplicates')}</button>
                )}
            </section>
        </main>
    )
}

function Tile({label, value, sub}: { label: string; value: string; sub: string }) {
    return (
        <div className="stat-tile">
            <div className="small muted">{label}</div>
            <div className="stat-value">{value}</div>
            <div className="small muted">{sub}</div>
        </div>
    )
}

function Bars({title, rows, total, onPick}: {
    title: string
    rows: catalog.SizeCount[]
    total: number
    onPick?: (r: catalog.SizeCount) => void
}) {
    const {t} = useTranslation()
    if (rows.length === 0) return null
    const max = Math.max(...rows.map(r => r.bytes), 1)
    return (
        <section className="card">
            <h2>{title}</h2>
            <ul className="plain bars">
                {rows.map(r => (
                    <li key={r.key + r.id}>
                        <button className="bar-row" disabled={!onPick} onClick={() => onPick?.(r)}
                                title={t('stats.barTitle', {name: r.key, files: formatNumber(r.files)})}>
                            <span className="bar-label ellipsis">{r.key}</span>
                            <span className="bar-track" aria-hidden="true">
                                <span className="bar-fill" style={{width: `${Math.max(1, r.bytes / max * 100)}%`}}/>
                            </span>
                            <span className="bar-value">{formatBytes(r.bytes)}
                                <span className="muted"> · {total > 0 ? Math.round(r.bytes / total * 100) : 0} %</span></span>
                        </button>
                    </li>
                ))}
            </ul>
        </section>
    )
}
