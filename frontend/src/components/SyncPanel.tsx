import {useCallback, useEffect, useState} from 'react'
import {useTranslation} from 'react-i18next'
import {GetSyncStatus, PickFolder, SetSyncDir, SyncNow} from '../../wailsjs/go/main/App'
import {main} from '../../wailsjs/go/models'
import {EventsOn} from '../../wailsjs/runtime/runtime'
import {formatDateTime} from '../format'
import {Msg, toMsg, translateMsg} from '../i18n/msg'
import {PathText} from './PathText'

/**
 * Sync with other PCs through a shared folder (J9): each PC puts its
 * cataloging work there as a file and takes over what the others changed.
 */
export function SyncPanel() {
    const {t} = useTranslation()
    const [st, setSt] = useState<main.SyncStatus | null>(null)
    const [error, setError] = useState<Msg | null>(null)
    const load = useCallback(() => {
        GetSyncStatus().then(setSt).catch(e => setError(toMsg(e)))
    }, [])
    useEffect(() => {
        load()
        const off = EventsOn('sync:changed', load)
        const timer = window.setInterval(load, 15000)
        return () => {
            off()
            window.clearInterval(timer)
        }
    }, [load])

    const choose = async () => {
        setError(null)
        try {
            const dir = await PickFolder()
            if (dir) setSt(await SetSyncDir(dir))
        } catch (e) {
            setError(toMsg(e))
        }
    }
    const off = async () => {
        setError(null)
        try {
            setSt(await SetSyncDir(''))
        } catch (e) {
            setError(toMsg(e))
        }
    }

    return (
        <section className="card">
            <h2>{t('sync.title')}</h2>
            <p className="small muted">{t('sync.intro')}</p>
            {!st?.dir ? (
                <button className="primary" onClick={choose}>{t('sync.choose')}…</button>
            ) : <>
                <p className="small"><span className="muted">{t('sync.folder')}:</span> <code><PathText path={st.dir}/></code></p>
                <p className="small muted">
                    {st.running ? t('sync.running')
                        : st.lastRun ? t('sync.lastRun', {when: formatDateTime(st.lastRun)}) : t('sync.never')}
                    {st.waiting && <> · {t('sync.waiting')}</>}
                </p>
                {st.error && <p className="error small">{translateMsg(st.error as Msg)}</p>}
                <h3>{t('sync.peers')}</h3>
                {st.peers.length === 0 ? <p className="small muted">{t('sync.noPeers')}</p> : (
                    <ul className="plain small">
                        {st.peers.map(p => (
                            <li key={p.name}>
                                <strong>{p.name}</strong>{' '}
                                <span className="muted">
                                    {t('sync.peerFile', {when: formatDateTime(p.modified)})}
                                    {' · '}{p.appliedAt ? t('sync.peerApplied', {when: formatDateTime(p.appliedAt)}) : t('sync.peerNew')}
                                </span>
                                {p.error && <div className="error">{p.error}</div>}
                            </li>
                        ))}
                    </ul>
                )}
                <div className="row wrap">
                    <button className="primary" disabled={st.running} onClick={() => SyncNow().then(() => window.setTimeout(load, 500))}>
                        {t('sync.now')}</button>
                    <button onClick={choose}>{t('sync.change')}…</button>
                    <button className="subtle" onClick={off}>{t('sync.off')}</button>
                </div>
                <p className="small muted">{t('sync.howItWorks')}</p>
            </>}
            {error && <p className="error">{translateMsg(error)}</p>}
        </section>
    )
}
