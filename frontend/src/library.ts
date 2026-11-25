import {useCallback, useEffect, useState} from 'react'
import {GetFilterOptions, ListRoots, ListTags, SearchModels} from '../wailsjs/go/main/App'
import {catalog} from '../wailsjs/go/models'
import {EventsOn} from '../wailsjs/runtime/runtime'
import {emptyQuery, Query} from './components/FilterPanel'

/** What the sidebar and the filters show about the library. */
export interface Library {
    tags: catalog.TagCount[]
    creators: catalog.Option[]
    exts: catalog.Option[]
    roots: catalog.Root[]
    counts: { all: number; untagged: number; favorites: number; waiting: number }
    /** Bumps whenever the catalog changed (scan finished, edits). */
    version: number
    /** Call after an edit (tags, favorites, names …). */
    reload: () => void
}

const count = (q: Partial<Query>) =>
    SearchModels(catalog.ModelQuery.createFrom({...emptyQuery, ...q}), 0, 1).then(p => p.total).catch(() => 0)

/** Loads the library overview and reloads it after scans and edits. */
export function useLibrary(): Library {
    const [version, setVersion] = useState(0)
    const [data, setData] = useState<Omit<Library, 'version' | 'reload'>>({
        tags: [], creators: [], exts: [], roots: [], counts: {all: 0, untagged: 0, favorites: 0, waiting: 0},
    })
    const reload = useCallback(() => setVersion(v => v + 1), [])

    useEffect(() => {
        let stale = false
        Promise.all([
            ListTags().catch(() => [] as catalog.TagCount[]),
            GetFilterOptions().catch(() => null),
            ListRoots().catch(() => [] as catalog.Root[]),
            count({}), count({untagged: true}), count({favorites: true}), count({waiting: true}),
        ]).then(([tags, opts, roots, all, untagged, favorites, waiting]) => {
            if (stale) return
            setData({
                tags, creators: opts?.creators ?? [], exts: opts?.exts ?? [], roots,
                counts: {all, untagged, favorites, waiting},
            })
        })
        return () => {
            stale = true
        }
    }, [version])

    // Scans (and detection) change everything; roots change their state.
    useEffect(() => {
        const offScan = EventsOn('scan:progress', (p: { running: boolean }) => {
            if (!p.running) reload()
        })
        const offRoots = EventsOn('roots:changed', reload)
        return () => {
            offScan()
            offRoots()
        }
    }, [reload])

    return {...data, version, reload}
}
