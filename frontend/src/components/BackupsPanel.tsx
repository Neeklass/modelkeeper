import {useEffect, useState} from 'react'
import {useTranslation} from 'react-i18next'
import {CancelRestore, ListBackups, Quit, RequestRestore} from '../../wailsjs/go/main/App'
import {main} from '../../wailsjs/go/models'
import {Msg, toMsg, translateMsg} from '../i18n/msg'
import {formatBytes, formatDateTime, formatNumber} from '../format'
import {useConfirm} from './ConfirmDialog'

/** Translation key of a snapshot reason ("pre-migration-0007" → backup.reason.preMigration). */
export function backupReasonKey(reason: string): string {
    if (reason.startsWith('pre-migration-')) return 'backup.reason.preMigration'
    return {
        'daily': 'backup.reason.daily',
        'manual': 'backup.reason.manual',
        'pre-remove-root': 'backup.reason.preRemoveRoot',
        'pre-filetypes': 'backup.reason.preFileTypes',
        'pre-restore': 'backup.reason.preRestore',
    }[reason] ?? 'backup.reason.other'
}

/**
 * Snapshots in data\backups (J2): a list to restore one from at the next
 * start, and the pending restore with "quit now" / "cancel".
 * Works without an open catalog (startup error screen).
 */
export function BackupsPanel({pending, onChanged, defaultOpen}: { pending: string; onChanged: () => void; defaultOpen?: boolean }) {
    const {t} = useTranslation()
    const [open, setOpen] = useState(!!defaultOpen)
    const [list, setList] = useState<main.BackupInfo[] | null>(null)
    const [busy, setBusy] = useState(false)
    const [error, setError] = useState<Msg | null>(null)
    const confirm = useConfirm()

    const run = async (fn: () => Promise<unknown>) => {
        setError(null)
        setBusy(true)
        try {
            await fn()
        } catch (e) {
            setError(toMsg(e))
        } finally {
            setBusy(false)
        }
    }

    useEffect(() => {
        if (defaultOpen) ListBackups().then(setList).catch(e => setError(toMsg(e)))
    }, [defaultOpen])

    const toggle = () => {
        if (!open) run(async () => setList(await ListBackups()))
        setOpen(o => !o)
    }

    const restore = (b: main.BackupInfo) => run(async () => {
        const when = formatDateTime(b.time)
        if (!await confirm({message: t('backup.confirmRestore', {when}), confirmLabel: t('backup.restoreConfirm'), danger: true})) return
        await RequestRestore(b.name)
        onChanged()
    })

    const pendingInfo = list?.find(b => b.name === pending)

    return (
        <div className="backups">
            {pending && (
                <div className="notice-inactive">
                    <p className="small">
                        {t('backup.pending', {when: pendingInfo ? formatDateTime(pendingInfo.time) : pending})}
                    </p>
                    <div className="row">
                        <button onClick={() => Quit()}>{t('backup.quitNow')}</button>
                        <button onClick={() => run(async () => { await CancelRestore(); onChanged() })} disabled={busy}>
                            {t('backup.cancelRestore')}
                        </button>
                    </div>
                </div>
            )}
            {!defaultOpen && <button className="link" onClick={toggle} aria-expanded={open}>
                {open ? '▾' : '▸'} {t('backup.listTitle')}
            </button>}
            {open && list && (list.length === 0
                ? <p className="small muted">{t('backup.none')}</p>
                : <ul className="plain small backup-list">
                    {list.map(b => (
                        <li key={b.name} className="row spread">
                            <span>
                                {formatDateTime(b.time)} · {t(backupReasonKey(b.reason))}
                                <span className="muted">
                                    {' · '}{t('backup.counts', {models: formatNumber(b.models), tags: formatNumber(b.tags)})}
                                    {' · '}{formatBytes(b.bytes ?? 0)}
                                </span>
                                {!b.verified && <span className="warn-text" title={t('backup.unverifiedHint')}> · {t('backup.unverified')}</span>}
                            </span>
                            <button className="link small" onClick={() => restore(b)} disabled={busy || b.name === pending}>
                                {t('backup.restore')}
                            </button>
                        </li>
                    ))}
                </ul>)}
            {error && <p className="error">{translateMsg(error)}</p>}
        </div>
    )
}
