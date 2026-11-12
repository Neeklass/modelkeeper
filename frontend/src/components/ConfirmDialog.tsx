import {createContext, ReactNode, useCallback, useContext, useEffect, useRef, useState} from 'react'
import {useTranslation} from 'react-i18next'

/** What a confirmation asks. The first paragraph of message is the title. */
export interface ConfirmOptions {
    message: string
    confirmLabel?: string
    /** Destructive or hard to undo: red button, Cancel has the focus. */
    danger?: boolean
}

type Confirm = (o: ConfirmOptions) => Promise<boolean>

const ConfirmContext = createContext<Confirm>(async o => window.confirm(o.message))

/** Returns a function that shows an in-app confirmation and resolves to the answer. */
export function useConfirm(): Confirm {
    return useContext(ConfirmContext)
}

/**
 * Provides useConfirm: one modal confirmation at a time, instead of the
 * browser's confirm(), which WebView2 shows as a web page popup.
 */
export function ConfirmProvider({children}: { children: ReactNode }) {
    const [pending, setPending] = useState<{ o: ConfirmOptions; resolve: (v: boolean) => void } | null>(null)
    const confirm = useCallback<Confirm>(o => new Promise(resolve => setPending({o, resolve})), [])
    const answer = (v: boolean) => {
        pending?.resolve(v)
        setPending(null)
    }
    return (
        <ConfirmContext.Provider value={confirm}>
            {children}
            {pending && <Dialog o={pending.o} onAnswer={answer}/>}
        </ConfirmContext.Provider>
    )
}

function Dialog({o, onAnswer}: { o: ConfirmOptions; onAnswer: (v: boolean) => void }) {
    const {t} = useTranslation()
    const cancelRef = useRef<HTMLButtonElement>(null)
    const okRef = useRef<HTMLButtonElement>(null)
    // Title: the first paragraph, or the question that starts a one-paragraph message.
    let [title, ...rest] = o.message.split('\n\n')
    const question = rest.length === 0 && title.match(/^([^?]+\?)\s+([\s\S]+)$/)
    if (question) [title, rest] = [question[1], [question[2]]]
    const body = rest.join('\n\n')

    useEffect(() => {
        (o.danger ? cancelRef : okRef).current?.focus()
        const onKey = (e: KeyboardEvent) => {
            if (e.key === 'Escape') onAnswer(false)
        }
        window.addEventListener('keydown', onKey)
        return () => window.removeEventListener('keydown', onKey)
    }, [o, onAnswer])

    return (
        <div className="modal-backdrop" onMouseDown={e => e.target === e.currentTarget && onAnswer(false)}>
            <div className="modal" role="alertdialog" aria-modal="true" aria-labelledby="confirm-title"
                 aria-describedby={body ? 'confirm-body' : undefined}>
                <h2 id="confirm-title">{title}</h2>
                {body && <p id="confirm-body" className="modal-body">{body}</p>}
                <div className="row modal-actions">
                    <button ref={cancelRef} onClick={() => onAnswer(false)}>{t('confirm.cancel')}</button>
                    <button ref={okRef} className={o.danger ? 'danger-solid' : 'primary'} onClick={() => onAnswer(true)}>
                        {o.confirmLabel ?? t('confirm.ok')}
                    </button>
                </div>
            </div>
        </div>
    )
}
