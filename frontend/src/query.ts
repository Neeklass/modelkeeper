// Pure query logic of the library (no React, no Wails): what the filters
// and sidebar views mean. Tested in tests/query.test.ts.

/** All list criteria (H1, H2, G4); mirrors catalog.ModelQuery. */
export interface Query {
    text: string
    untagged: boolean
    favorites: boolean
    tagId: number
    creatorId: number
    rootId: number
    ext: string
    supports: string
    printStatus: string
    duplicates: boolean
    singleLocation: boolean
    offline: boolean
    brokenArchives: boolean
    extractedAndArchive: boolean
    missing: boolean
    inactive: boolean
    /** Models without files in the catalog yet (imported, J7). */
    waiting: boolean
    orphanedBy: number
    /** "" = by name, "newest" = most recently added first. */
    sort: string
}

export const emptyQuery: Query = {
    text: '', untagged: false, favorites: false, tagId: 0, creatorId: 0, rootId: 0, ext: '', supports: '', printStatus: '',
    duplicates: false, singleLocation: false, offline: false, brokenArchives: false,
    extractedAndArchive: false, missing: false, inactive: false, waiting: false, orphanedBy: 0, sort: '',
}

export const FLAGS = ['duplicates', 'singleLocation', 'offline', 'brokenArchives', 'extractedAndArchive',
    'missing', 'inactive'] as const

/** Fields that choose the view (sidebar) rather than filter it. */
const VIEW_FIELDS = new Set(['text', 'favorites', 'untagged', 'sort', 'orphanedBy', 'waiting'])

/** Number of active filters, not counting the search text and the view (favorites, to catalog, sort). */
export function activeFilters(q: Query): number {
    let n = 0
    for (const [k, v] of Object.entries(q)) {
        if (!VIEW_FIELDS.has(k) && v !== (emptyQuery as unknown as Record<string, unknown>)[k]) n++
    }
    return n
}

/** The query without its filters; the search text and the view stay. */
export function clearFilters(q: Query): Query {
    const next = {...emptyQuery} as unknown as Record<string, unknown>
    for (const k of VIEW_FIELDS) next[k] = (q as unknown as Record<string, unknown>)[k]
    return next as unknown as Query
}

export type LibraryView = 'all' | 'untagged' | 'favorites' | 'newest' | 'waiting'

/** The view of the library a query shows (sidebar entries). */
export function libraryView(q: Query): LibraryView {
    if (q.waiting) return 'waiting'
    if (q.untagged) return 'untagged'
    if (q.favorites) return 'favorites'
    if (q.sort === 'newest') return 'newest'
    return 'all'
}

export const VIEW_QUERY: Record<LibraryView, Partial<Query>> = {
    all: {},
    untagged: {untagged: true},
    favorites: {favorites: true},
    newest: {sort: 'newest'},
    waiting: {waiting: true},
}
