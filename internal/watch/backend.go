package watch

import "github.com/fsnotify/fsnotify"

// backend delivers file system events. On Windows it watches each root with
// one recursive handle (backend_windows.go): fsnotify's per-folder handles
// there keep Windows from renaming or moving a folder that has subfolders
// while the app runs. Elsewhere fsnotify watches every folder (inotify
// handles do not block anything).
type backend interface {
	Add(path string) error
	Remove(path string) error
	WatchList() []string
	Events() <-chan fsnotify.Event
	Errors() <-chan error
	Close() error
	// Recursive: Add watches the whole tree below the path.
	Recursive() bool
}

// notifyBackend is fsnotify, one watch per folder.
type notifyBackend struct{ w *fsnotify.Watcher }

func newNotifyBackend() (backend, error) {
	w, err := fsnotify.NewBufferedWatcher(4096)
	if err != nil {
		return nil, err
	}
	return notifyBackend{w}, nil
}

func (b notifyBackend) Add(p string) error            { return b.w.Add(p) }
func (b notifyBackend) Remove(p string) error         { return b.w.Remove(p) }
func (b notifyBackend) WatchList() []string           { return b.w.WatchList() }
func (b notifyBackend) Events() <-chan fsnotify.Event { return b.w.Events }
func (b notifyBackend) Errors() <-chan error          { return b.w.Errors }
func (b notifyBackend) Close() error                  { return b.w.Close() }
func (b notifyBackend) Recursive() bool               { return false }
