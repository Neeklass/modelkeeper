import {createContext, ReactNode, useCallback, useContext, useEffect, useRef, useState} from 'react'
import {useTranslation} from 'react-i18next'
import {Undo} from '../../wailsjs/go/main/App'
import {toMsg, translateMsg} from '../i18n/msg'

/** A short message at the bottom of the window, optionally with "Undo" (TODO 2.1). */
export interface ToastOptions {
    text: string
    /** Offer "Undo" for the change that was just made. */
    undo?: boolean
    error?: boolean
}

interface ToastApi {
    show: (o: ToastOptions) => void
    /** Undoes the newest change and tells what was undone. */
    undo: () => Promise<void>
}

const ToastContext = createContext<ToastApi>({show: () => undefined, undo: async () => undefined})

export function useToast(): ToastApi {
    return useContext(ToastContext)
}

/** Event the app listens to: the catalog changed outside the normal flow (undo). */
export const CATALOG_CHANGED = 'modelkeeper:catalog-changed'

const SHOW_MS = 8000

export function ToastProvider({children}: { children: ReactNode }) {
    const {t} = useTranslation()
    const [toast, setToast] = useState<(ToastOptions & { id: number }) | null>(null)
    const timer = useRef<number | undefined>(undefined)
    const seq = useRef(0)

    const show = useCallback((o: ToastOptions) => {
        window.clearTimeout(timer.current)
        const id = ++seq.current
        setToast({...o, id})
        timer.current = window.setTimeout(() => setToast(cur => cur?.id === id ? null : cur), SHOW_MS)
    }, [])

    const undo = useCallback(async () => {
        try {
            const r = await Undo()
            window.dispatchEvent(new Event(CATALOG_CHANGED))
            const what = t(r.action, {...(r.params ?? {}), defaultValue: r.action})
            show({text: r.skipped > 0
                    ? t('history.undoneSkipped', {what, count: r.skipped, n: r.skipped})
                    : t('history.undone', {what})})
        } catch (e) {
            show({text: translateMsg(toMsg(e)), error: true})
        }
    }, [show, t])

    useEffect(() => () => window.clearTimeout(timer.current), [])

    return (
        <ToastContext.Provider value={{show, undo}}>
            {children}
            {toast && (
                <div className={`toast${toast.error ? ' toast-error' : ''}`} role="status" aria-live="polite">
                    <span>{toast.text}</span>
                    {toast.undo && <button className="link" onClick={() => {
                        setToast(null)
                        undo()
                    }}>{t('history.undo')}</button>}
                    <button className="chip-x" aria-label={t('history.dismiss')} onClick={() => setToast(null)}>✕</button>
                </div>
            )}
        </ToastContext.Provider>
    )
}
