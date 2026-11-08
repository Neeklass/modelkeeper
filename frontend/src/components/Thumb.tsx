import {useEffect, useState} from 'react'

/** Thumbnail image, or a neutral placeholder while none exists (I1). */
export function Thumb({src, className}: { src: string; className: string }) {
    const [failed, setFailed] = useState(false)
    useEffect(() => setFailed(false), [src])
    if (!src || failed) return <div className={`${className} thumb-empty`} aria-hidden="true"/>
    return <img className={className} src={src} alt="" loading="lazy" decoding="async" onError={() => setFailed(true)}/>
}
