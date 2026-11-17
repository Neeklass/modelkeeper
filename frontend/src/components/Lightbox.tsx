import {useEffect} from 'react'
import {useTranslation} from 'react-i18next'

export interface LightboxImage {
    src: string
    label: string
}

/** A picture over the whole window, with ← / → between the model's pictures and Esc to close. */
export function Lightbox({images, index, onIndex, onClose}: {
    images: LightboxImage[]
    index: number
    onIndex: (i: number) => void
    onClose: () => void
}) {
    const {t} = useTranslation()
    const n = images.length
    const go = (d: number) => onIndex((index + d + n) % n)
    useEffect(() => {
        const onKey = (e: KeyboardEvent) => {
            if (e.key === 'Escape') onClose()
            else if (e.key === 'ArrowRight') go(1)
            else if (e.key === 'ArrowLeft') go(-1)
            else return
            e.preventDefault()
            e.stopPropagation()
        }
        window.addEventListener('keydown', onKey, true)
        return () => window.removeEventListener('keydown', onKey, true)
    })
    const img = images[index]
    return (
        <div className="lightbox" role="dialog" aria-modal="true" aria-label={img.label} onClick={onClose}>
            <img src={img.src} alt={img.label} onClick={e => e.stopPropagation()}/>
            <div className="lightbox-bar" onClick={e => e.stopPropagation()}>
                {n > 1 && <button className="icon" onClick={() => go(-1)} aria-label={t('gallery.prev')}>‹</button>}
                <span className="ellipsis">{img.label}{n > 1 && <span className="muted"> · {index + 1} / {n}</span>}</span>
                {n > 1 && <button className="icon" onClick={() => go(1)} aria-label={t('gallery.next')}>›</button>}
                <button className="icon" onClick={onClose} aria-label={t('models.close')}>✕</button>
            </div>
        </div>
    )
}
