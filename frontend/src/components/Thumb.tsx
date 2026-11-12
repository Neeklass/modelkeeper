import {useEffect, useState} from 'react'

/** Up to two initials of a model name, as placeholder text ("Orc Warlord" → "OW"). */
function initials(name: string): string {
    return name.split(/[\s_\-.]+/).filter(w => /\p{L}|\p{N}/u.test(w)).slice(0, 2)
        .map(w => [...w][0].toUpperCase()).join('')
}

/** Thumbnail image, or a placeholder with the model's initials while none exists (I1). */
export function Thumb({src, className, label}: { src: string; className: string; label?: string }) {
    const [failed, setFailed] = useState(false)
    useEffect(() => setFailed(false), [src])
    if (!src || failed) {
        return <div className={`${className} thumb-empty`} aria-hidden="true">{label ? initials(label) : null}</div>
    }
    return <img className={className} src={src} alt="" loading="lazy" decoding="async" onError={() => setFailed(true)}/>
}
