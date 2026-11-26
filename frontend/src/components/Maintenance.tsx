import {useCallback, useEffect, useState} from 'react'
import {useTranslation} from 'react-i18next'
import {CheckHealthNow, ExportDiagnostics, GetHealth} from '../../wailsjs/go/main/App'
import {health} from '../../wailsjs/go/models'
import {EventsOn} from '../../wailsjs/runtime/runtime'
import {formatBytes, formatDateTime, formatNumber} from '../format'
import {Msg, toMsg, translateMsg} from '../i18n/msg'
import {PathText} from './PathText'

/** The last health report, kept up to date (null while loading or if none). */
export function useHealth(): health.Report | null {
    const [r, setR] = useState<health.Report | null>(null)
    useEffect(() => {
        const load = () => GetHealth().then(x => setR(x ?? null)).catch(() => setR(null))
        load()
        return EventsOn('health:changed', load)
    }, [])
    return r
}

/** Maintenance check (TODO 5.3): integrity, compacting, thumbnails, disk space. */
export function HealthPanel() {
    const {t} = useTranslation()
    const [report, setReport] = useState<health.Report | null>(null)
    const [busy, setBusy] = useState(false)
    const [error, setError] = useState<Msg | null>(null)
    const load = useCallback(() => {
        GetHealth().then(r => setReport(r ?? null)).catch(() => {})
    }, [])
    useEffect(() => {
        load()
        return EventsOn('health:changed', load)
    }, [load])
    const run = async () => {
        setBusy(true)
        setError(null)
        try {
            setReport(await CheckHealthNow())
        } catch (e) {
            setError(toMsg(e))
        } finally {
            setBusy(false)
        }
    }
    const r = report
    return (
        <section className="card">
            <h2>{t('health.title')}</h2>
            <p className="small muted">{t('health.intro')}</p>
            {r ? (
                <ul className="plain small health-list">
                    <li>{t('health.lastRun', {when: formatDateTime(r.time)})}</li>
                    <li className={r.integrityOk ? 'ok-text' : 'error'}>
                        {r.integrityOk ? `✓ ${t('health.integrityOk')}` : `✗ ${t('health.integrityBad')}`}
                    </li>
                    {!r.integrityOk && r.problem && <li><code className="small">{r.problem}</code></li>}
                    <li>{t('health.catalog', {size: formatBytes(r.dbBytes)})}
                        {r.compacted && ` · ${t('health.compacted', {size: formatBytes(r.freed)})}`}</li>
                    <li>{t('health.thumbnails', {count: r.thumbnails, size: formatBytes(r.thumbnailBytes), n: formatNumber(r.thumbnails)})}
                        {r.orphaned > 0 && ` · ${t('health.orphaned', {count: r.orphaned})}`}
                        {r.purged > 0 && ` · ${t('health.purged', {count: r.purged})}`}</li>
                    {r.freeBytes >= 0 && <li className={r.lowSpace ? 'warn-text' : ''}>
                        {r.lowSpace ? '⚠ ' : ''}{t('health.disk', {free: formatBytes(r.freeBytes), total: formatBytes(r.totalBytes)})}
                    </li>}
                </ul>
            ) : <p className="small muted">{t('health.never')}</p>}
            {r && !r.integrityOk && <p className="small error">{t('health.integrityHelp')}</p>}
            {r?.lowSpace && <p className="small warn-text">{t('health.lowSpaceHelp')}</p>}
            <div className="row">
                <button onClick={run} disabled={busy}>{busy ? t('health.running') : t('health.runNow')}</button>
            </div>
            <p className="small muted hint">{t('health.weekly')}</p>
            {error && <p className="error">{translateMsg(error)}</p>}
        </section>
    )
}

/** Diagnostics archive to send along with a problem (TODO 5.4). */
export function DiagnosticsButton() {
    const {t} = useTranslation()
    const [done, setDone] = useState<string | null>(null)
    const [error, setError] = useState<Msg | null>(null)
    const run = async () => {
        setError(null)
        try {
            const p = await ExportDiagnostics()
            if (p) setDone(p)
        } catch (e) {
            setError(toMsg(e))
        }
    }
    return (
        <div>
            <button onClick={run}>{t('diag.export')}</button>
            <p className="small muted hint">{t('diag.hint')}</p>
            {done && <p className="small">{t('diag.done')} <PathText path={done}/></p>}
            {error && <p className="error">{translateMsg(error)}</p>}
        </div>
    )
}

export function DiagnosticsPanel() {
    const {t} = useTranslation()
    return (
        <section className="card">
            <h2>{t('diag.title')}</h2>
            <DiagnosticsButton/>
        </section>
    )
}
