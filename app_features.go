package main

import (
	"modelkeeper/internal/catalog"
)

// Print log, collections, saved searches, bulk editing, storage statistics.

// GetStats returns the storage overview (sizes per folder, creator, type;
// duplicates).
func (a *App) GetStats() (catalog.Stats, error) {
	if err := a.ready(); err != nil {
		return catalog.Stats{}, err
	}
	s, err := catalog.GetStats(a.ctx, a.db)
	return s, wrapDB(err)
}

// AddPrint logs a print of a model.
func (a *App) AddPrint(p catalog.Print) (catalog.Print, error) {
	if err := a.ready(); err != nil {
		return p, err
	}
	r, err := catalog.AddPrint(a.ctx, a.db, p)
	return r, wrapDB(err)
}

// DeletePrint removes an entry of the print log.
func (a *App) DeletePrint(id int64) error {
	if err := a.ready(); err != nil {
		return err
	}
	return wrapDB(catalog.DeletePrint(a.ctx, a.db, id))
}

// UpdateModels changes fields of many models at once (undoable).
func (a *App) UpdateModels(ids []int64, u catalog.ModelUpdate) error {
	if err := a.ready(); err != nil {
		return err
	}
	return wrapDB(catalog.UpdateModels(a.ctx, a.db, ids, u))
}

// ListCollections returns all collections.
func (a *App) ListCollections() ([]catalog.Collection, error) {
	if err := a.ready(); err != nil {
		return nil, err
	}
	l, err := catalog.ListCollections(a.ctx, a.db)
	return l, wrapDB(err)
}

// CreateCollection creates a collection (or returns the one of that name).
func (a *App) CreateCollection(name string) (catalog.Collection, error) {
	if err := a.ready(); err != nil {
		return catalog.Collection{}, err
	}
	c, err := catalog.CreateCollection(a.ctx, a.db, name)
	return c, wrapDB(err)
}

// RenameCollection renames a collection.
func (a *App) RenameCollection(id int64, name string) error {
	if err := a.ready(); err != nil {
		return err
	}
	return wrapDB(catalog.RenameCollection(a.ctx, a.db, id, name))
}

// DeleteCollection removes a collection (its models stay).
func (a *App) DeleteCollection(id int64) error {
	if err := a.ready(); err != nil {
		return err
	}
	return wrapDB(catalog.DeleteCollection(a.ctx, a.db, id))
}

// AddToCollection appends models to a collection; returns the number added.
func (a *App) AddToCollection(id int64, modelIDs []int64) (int, error) {
	if err := a.ready(); err != nil {
		return 0, err
	}
	n, err := catalog.AddToCollection(a.ctx, a.db, id, modelIDs)
	return n, wrapDB(err)
}

// RemoveFromCollection takes models out of a collection.
func (a *App) RemoveFromCollection(id int64, modelIDs []int64) error {
	if err := a.ready(); err != nil {
		return err
	}
	return wrapDB(catalog.RemoveFromCollection(a.ctx, a.db, id, modelIDs))
}

// MoveInCollection moves a model one place up (delta < 0) or down.
func (a *App) MoveInCollection(id, modelID int64, delta int) error {
	if err := a.ready(); err != nil {
		return err
	}
	return wrapDB(catalog.MoveInCollection(a.ctx, a.db, id, modelID, delta))
}

// ListSavedSearches returns the saved searches.
func (a *App) ListSavedSearches() ([]catalog.SavedSearch, error) {
	if err := a.ready(); err != nil {
		return nil, err
	}
	l, err := catalog.ListSavedSearches(a.ctx, a.db)
	return l, wrapDB(err)
}

// SaveSearch stores a search under a name (the query as JSON).
func (a *App) SaveSearch(name, query string) (catalog.SavedSearch, error) {
	if err := a.ready(); err != nil {
		return catalog.SavedSearch{}, err
	}
	s, err := catalog.SaveSearch(a.ctx, a.db, name, query)
	return s, wrapDB(err)
}

// DeleteSavedSearch removes a saved search.
func (a *App) DeleteSavedSearch(id int64) error {
	if err := a.ready(); err != nil {
		return err
	}
	return wrapDB(catalog.DeleteSavedSearch(a.ctx, a.db, id))
}
