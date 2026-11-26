import {useCallback, useEffect, useState} from 'react'
import {useTranslation} from 'react-i18next'
import {
    CheckForUpdateNow, CheckHealthNow, ExportDiagnostics, GetHealth, GetLANStatus, GetUpdateStatus, NewLANKey, OpenReleasePage,
    SetLANView, SetUpdateCheck,
} from '../../wailsjs/go/main/App'
import {health, main} from '../../wailsjs/go/models'
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

/** Read-only view for a phone or tablet at home (TODO 6.3). */
export function LANPanel() {
    const {t} = useTranslation()
    const [st, setSt] = useState<main.LANStatus | null>(null)
    const [port, setPort] = useState('')
    const [error, setError] = useState<Msg | null>(null)
    useEffect(() => {
        GetLANStatus().then(s => {
            setSt(s)
            setPort(String(s.port))
        }).catch(e => setError(toMsg(e)))
    }, [])
    const run = async (fn: () => Promise<main.LANStatus>) => {
        setError(null)
        try {
            setSt(await fn())
        } catch (e) {
            setError(toMsg(e))
        }
    }
    const portNum = Number(port) || 0
    return (
        <section className="card">
            <h2>{t('lan.title')}</h2>
            <p className="small muted">{t('lan.intro')}</p>
            <label className="row small">
                <input type="checkbox" checked={!!st?.enabled}
                       onChange={e => run(() => SetLANView(e.target.checked, portNum))}/>
                {t('lan.enable')}
            </label>
            <label className="row small">
                {t('lan.port')}
                <input className="small" style={{width: '6em'}} value={port} inputMode="numeric"
                       onChange={e => setPort(e.target.value.replace(/[^0-9]/g, ''))}
                       onBlur={() => st?.enabled && portNum !== st.port && run(() => SetLANView(true, portNum))}/>
            </label>
            {st?.running && (
                <div className="small">
                    <p>{t('lan.links')}</p>
                    <ul className="plain">
                        {st.links.map(l => <li key={l}><code className="selectable">{l}</code></li>)}
                    </ul>
                    {st.links.length === 0 && <p className="muted">{t('lan.noAddress')}</p>}
                    <button className="link small" onClick={() => run(() => NewLANKey())}>{t('lan.newKey')}</button>
                    <p className="muted hint">{t('lan.keyHint')}</p>
                </div>
            )}
            {st?.enabled && !st.running && st.error && <p className="error small">{translateMsg(st.error as Msg)}</p>}
            <p className="small muted hint">{t('lan.firewall')}</p>
            {error && <p className="error">{translateMsg(error)}</p>}
        </section>
    )
}

/** Looks for a new release (only reading GitHub; installing stays with the user). */
export function UpdatePanel() {
    const {t} = useTranslation()
    const [st, setSt] = useState<main.UpdateStatus | null>(null)
    const [busy, setBusy] = useState(false)
    useEffect(() => {
        GetUpdateStatus().then(setSt).catch(() => {})
    }, [])
    const check = async () => {
        setBusy(true)
        try {
            setSt(await CheckForUpdateNow())
        } finally {
            setBusy(false)
        }
    }
    return (
        <section className="card">
            <h2>{t('update.title')}</h2>
            <p className="small">{t('update.current', {version: st?.current ?? '…'})}</p>
            {st?.latest && (st.newer
                ? <p className="small notice-inactive">{t('update.newer', {version: st.latest.version})}{' '}
                    <button className="link small" onClick={() => OpenReleasePage()}>{t('update.open')}</button></p>
                : <p className="small muted">{t('update.upToDate', {version: st.latest.version})}</p>)}
            {st?.error && st.checkedAt > 0 && <p className="small muted">{translateMsg(st.error as Msg)}</p>}
            <div className="row">
                <button onClick={check} disabled={busy}>{busy ? t('update.checking') : t('update.checkNow')}</button>
                <label className="row small">
                    <input type="checkbox" checked={!!st?.enabled} onChange={async e => {
                        const on = e.target.checked
                        await SetUpdateCheck(on)
                        setSt(s => s ? main.UpdateStatus.createFrom({...s, enabled: on}) : s)
                    }}/>
                    {t('update.atStart')}
                </label>
            </div>
            <p className="small muted hint">{t('update.hint')}</p>
        </section>
    )
}
