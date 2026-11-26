import {test} from 'node:test'
import {strict as assert} from 'node:assert'
import {activeFilters, clearFilters, emptyQuery, FLAGS, libraryView, LibraryView, Query, VIEW_QUERY} from '../src/query'

test('every sidebar view is recognised from its own query', () => {
    for (const v of Object.keys(VIEW_QUERY) as LibraryView[]) {
        assert.equal(libraryView({...emptyQuery, ...VIEW_QUERY[v]}), v, v)
    }
})

test('views and the search text are not counted as filters', () => {
    for (const v of Object.keys(VIEW_QUERY) as LibraryView[]) {
        assert.equal(activeFilters({...emptyQuery, ...VIEW_QUERY[v], text: 'orc'}), 0, v)
    }
})

test('filters are counted and cleared, the view stays', () => {
    const q: Query = {...emptyQuery, favorites: true, text: 'orc', tagId: 3, printStatus: 'printed', duplicates: true}
    assert.equal(activeFilters(q), 3)
    const c = clearFilters(q)
    assert.equal(activeFilters(c), 0)
    assert.equal(c.favorites, true)
    assert.equal(c.text, 'orc')
    assert.equal(c.tagId, 0)
})

test('every flag filter is a boolean field of the query', () => {
    for (const f of FLAGS) {
        assert.equal(typeof (emptyQuery as unknown as Record<string, unknown>)[f], 'boolean', f)
        assert.equal(activeFilters({...emptyQuery, [f]: true}), 1, f)
    }
})
