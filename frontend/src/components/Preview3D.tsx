import {useEffect, useRef, useState} from 'react'
import {useTranslation} from 'react-i18next'
import {catalog} from '../../wailsjs/go/models'
import {formatBytes} from '../format'
import type {Viewer} from '../viewer/viewer'

/** File types the 3D preview can show (internal/preview.Exts). */
export const PREVIEW_EXTS = new Set(['stl', 'obj', '3mf', 'ply'])
/** Largest file the preview loads (internal/preview.MaxBytes). */
const MAX_BYTES = 300 * 1024 * 1024

export function canPreview(f: catalog.ModelFile): boolean {
    return f.present && PREVIEW_EXTS.has(f.ext) && f.size <= MAX_BYTES
}

/**
 * The file shown first: like the thumbnail choice (internal/thumbs), STLs
 * without supports first, then the largest file (usually the main body).
 */
export function defaultPreviewFile(files: catalog.ModelFile[]): catalog.ModelFile | undefined {
    const score = (f: catalog.ModelFile) => {
        const name = (f.path.split('/').pop() ?? '').toLowerCase()
        const stem = name.replace(/\.[^.]+$/, '')
        let s = f.ext === 'stl' ? 1000 : 500
        if (f.path.toLowerCase().includes('sup') || stem.endsWith('_ps') || stem.endsWith('-ps')) s -= 500
        return s + Math.min(f.size / 65536, 400)
    }
    return files.filter(canPreview).sort((a, b) => score(b) - score(a))[0]
}

type State = { kind: 'loading' } | { kind: 'ready' } | { kind: 'error'; key: string; detail?: string }

const STATUS_KEYS: Record<number, string> = {
    403: 'preview.encrypted',
    404: 'preview.notFound',
    413: 'preview.tooLarge',
    503: 'preview.offline',
}

/** Rotatable 3D preview of one of the model's files (H4). */
export function Preview3D({files, locationId, onSelect, onUseAsThumbnail, busy}: {
    files: catalog.ModelFile[]
    locationId: number
    onSelect: (locationId: number) => void
    onUseAsThumbnail: (dataURL: string) => void
    busy: boolean
}) {
    const {t} = useTranslation()
    const host = useRef<HTMLDivElement>(null)
    const viewer = useRef<Viewer | null>(null)
    const [state, setState] = useState<State>({kind: 'loading'})
    const previewable = files.filter(canPreview)
    const file = previewable.find(f => f.locationId === locationId)

    // One viewer (WebGL context) for the lifetime of the component.
    useEffect(() => {
        let cancelled = false
        import('../viewer/viewer').then(({Viewer}) => {
            if (!cancelled && host.current) viewer.current = new Viewer(host.current)
        })
        return () => {
            cancelled = true
            viewer.current?.dispose()
            viewer.current = null
        }
    }, [])

    useEffect(() => {
        if (!file) return
        const abort = new AbortController()
        setState({kind: 'loading'})
        ;(async () => {
            const [mod, res] = await Promise.all([
                import('../viewer/viewer'),
                fetch(`/files/${file.locationId}`, {signal: abort.signal}),
            ])
            if (!res.ok) {
                setState({kind: 'error', key: STATUS_KEYS[res.status] ?? 'preview.failed', detail: res.statusText})
                return
            }
            const data = await res.arrayBuffer()
            if (abort.signal.aborted) return
            // Give React a frame to show "loading" before parsing blocks.
            await new Promise(r => requestAnimationFrame(r))
            if (abort.signal.aborted || !viewer.current) return
            try {
                viewer.current.show(mod.parseModel(file.ext, data))
                setState({kind: 'ready'})
            } catch (e) {
                setState({kind: 'error', key: 'preview.parseFailed', detail: e instanceof Error ? e.message : String(e)})
            }
        })().catch(e => {
            if (!abort.signal.aborted) setState({kind: 'error', key: 'preview.failed', detail: String(e)})
        })
        return () => abort.abort()
    }, [file?.locationId]) // eslint-disable-line react-hooks/exhaustive-deps

    return (
        <div className="preview3d">
            <div className="preview3d-canvas" ref={host}>
                {state.kind !== 'ready' && (
                    <div className={`preview3d-overlay ${state.kind === 'error' ? 'error' : 'muted'}`}>
                        {state.kind === 'loading'
                            ? t('preview.loading', {size: file ? formatBytes(file.size) : ''})
                            : t(state.key, {detail: state.detail})}
                    </div>
                )}
            </div>
            <div className="row small spread">
                {previewable.length > 1 ? (
                    <select className="ellipsis" value={locationId} onChange={e => onSelect(Number(e.target.value))}
                            aria-label={t('preview.file')}>
                        {previewable.map(f => (
                            <option key={f.locationId} value={f.locationId}>
                                {f.variant ? `${f.variant} · ` : ''}{f.path.split('/').pop()} ({formatBytes(f.size)})
                            </option>
                        ))}
                    </select>
                ) : <span className="ellipsis muted">{file?.path.split('/').pop()}</span>}
                <span className="row nowrap">
                    <button className="link small" disabled={state.kind !== 'ready'}
                            onClick={() => viewer.current?.resetView()}>{t('preview.resetView')}</button>
                    <button className="link small" disabled={busy || state.kind !== 'ready'}
                            onClick={() => viewer.current && onUseAsThumbnail(viewer.current.capture())}>
                        {t('preview.useAsThumbnail')}
                    </button>
                </span>
            </div>
            <p className="small muted hint">{t('preview.hint')}</p>
        </div>
    )
}
