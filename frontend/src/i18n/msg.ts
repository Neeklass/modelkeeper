// Translation of backend messages (concept N4): the backend sends
// {key, params}; errors arrive as the JSON text of such a message.
import i18n from './index'

export interface Msg {
    key: string
    params?: Record<string, unknown>
}

/** Turns a rejected Wails call (or anything thrown) into a Msg. */
export function toMsg(err: unknown): Msg {
    const text = typeof err === 'string' ? err : err instanceof Error ? err.message : String(err)
    try {
        const parsed = JSON.parse(text)
        if (parsed && typeof parsed.key === 'string') return parsed as Msg
    } catch {
        // not a backend message
    }
    return {key: 'error.unknown', params: {detail: text}}
}

export function translateMsg(m: Msg): string {
    return i18n.t(m.key, {...m.params, defaultValue: m.key})
}
