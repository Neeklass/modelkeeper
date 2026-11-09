import {useState} from 'react'
import {useTranslation} from 'react-i18next'
import {SetFavorite} from '../../wailsjs/go/main/App'

/** Star that adds a model to the favorites or removes it. */
export function FavoriteStar({id, on, onChanged, className = ''}: {
    id: number
    on: boolean
    onChanged: () => void
    className?: string
}) {
    const {t} = useTranslation()
    const [busy, setBusy] = useState(false)
    const label = on ? t('favorites.remove') : t('favorites.add')
    return (
        <button className={`star${on ? ' on' : ''} ${className}`} aria-pressed={on} aria-label={label} title={label}
                disabled={busy}
                onClick={async e => {
                    e.stopPropagation() // not a row selection
                    setBusy(true)
                    try {
                        await SetFavorite([id], !on)
                        onChanged()
                    } finally {
                        setBusy(false)
                    }
                }}>
            {on ? '★' : '☆'}
        </button>
    )
}
