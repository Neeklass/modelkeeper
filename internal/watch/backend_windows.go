//go:build windows

package watch

import (
	"errors"
	"io/fs"
	"path/filepath"
	"sync"
	"unsafe"

	"github.com/fsnotify/fsnotify"
	"golang.org/x/sys/windows"
)

func newBackend() (backend, error) { return newRecursiveBackend(), nil }

// recursiveBackend watches each added folder with one handle and
// ReadDirectoryChangesW over the whole subtree. Only the root folder has an
// open handle, so folders below it can be renamed and moved freely.
type recursiveBackend struct {
	mu      sync.Mutex
	watches map[string]*dirWatch // cleaned path -> watch
	events  chan fsnotify.Event
	errors  chan error
	closed  bool
}

type dirWatch struct {
	path   string
	handle windows.Handle
	stopEv windows.Handle // set to stop the reader
	done   chan struct{}
}

func newRecursiveBackend() *recursiveBackend {
	return &recursiveBackend{
		watches: map[string]*dirWatch{},
		events:  make(chan fsnotify.Event, 4096),
		errors:  make(chan error, 16),
	}
}

const notifyFilter = windows.FILE_NOTIFY_CHANGE_FILE_NAME | windows.FILE_NOTIFY_CHANGE_DIR_NAME |
	windows.FILE_NOTIFY_CHANGE_SIZE | windows.FILE_NOTIFY_CHANGE_LAST_WRITE

func (b *recursiveBackend) Add(path string) error {
	path = filepath.Clean(path)
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return errors.New("watcher closed")
	}
	if _, ok := b.watches[path]; ok {
		return nil
	}
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	// FILE_SHARE_DELETE: the folder itself may be renamed or deleted by others.
	h, err := windows.CreateFile(p, windows.FILE_LIST_DIRECTORY,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OVERLAPPED, 0)
	if err != nil {
		return &fs.PathError{Op: "watch", Path: path, Err: err}
	}
	stopEv, err := windows.CreateEvent(nil, 1, 0, nil)
	if err != nil {
		windows.CloseHandle(h)
		return err
	}
	w := &dirWatch{path: path, handle: h, stopEv: stopEv, done: make(chan struct{})}
	b.watches[path] = w
	go b.read(w)
	return nil
}

// read waits for changes (overlapped ReadDirectoryChangesW) until the stop
// event is set; it owns and finally closes the folder handle.
func (b *recursiveBackend) read(w *dirWatch) {
	defer close(w.done)
	defer windows.CloseHandle(w.stopEv)
	defer windows.CloseHandle(w.handle)
	ioEv, err := windows.CreateEvent(nil, 1, 0, nil)
	if err != nil {
		b.sendError(&fs.PathError{Op: "watch", Path: w.path, Err: err})
		return
	}
	defer windows.CloseHandle(ioEv)
	buf := make([]uint32, 16*1024) // 64 KB, DWORD-aligned as the API requires
	raw := unsafe.Slice((*byte)(unsafe.Pointer(&buf[0])), len(buf)*4)
	for {
		ov := windows.Overlapped{HEvent: ioEv}
		windows.ResetEvent(ioEv)
		err := windows.ReadDirectoryChanges(w.handle, &raw[0], uint32(len(raw)), true, notifyFilter, nil, &ov, 0)
		if err != nil && !errors.Is(err, windows.ERROR_IO_PENDING) {
			b.sendError(&fs.PathError{Op: "watch", Path: w.path, Err: err})
			return
		}
		ev, err := windows.WaitForMultipleObjects([]windows.Handle{ioEv, w.stopEv}, false, windows.INFINITE)
		if err != nil || ev != windows.WAIT_OBJECT_0 {
			// Stopped (or the wait failed): cancel the read and wait for it,
			// so the buffer is not written after we return.
			windows.CancelIoEx(w.handle, &ov)
			var n uint32
			windows.GetOverlappedResult(w.handle, &ov, &n, true)
			return
		}
		var n uint32
		if err := windows.GetOverlappedResult(w.handle, &ov, &n, false); err != nil {
			if errors.Is(err, windows.ERROR_OPERATION_ABORTED) {
				return
			}
			b.sendError(&fs.PathError{Op: "watch", Path: w.path, Err: err})
			return
		}
		if n == 0 { // the buffer overflowed: events were lost
			b.sendError(errors.New("watch: too many changes at once (events lost)"))
			continue
		}
		b.parse(w.path, raw[:n])
	}
}

func (b *recursiveBackend) parse(dir string, buf []byte) {
	off := 0
	for off+12 <= len(buf) {
		info := (*windows.FileNotifyInformation)(unsafe.Pointer(&buf[off]))
		size := int(info.FileNameLength) / 2
		var name string
		if size > 0 && off+12+size*2 <= len(buf) {
			name = windows.UTF16ToString(unsafe.Slice(&info.FileName, size))
		}
		ev := fsnotify.Event{Name: filepath.Join(dir, name)}
		switch info.Action {
		case windows.FILE_ACTION_ADDED, windows.FILE_ACTION_RENAMED_NEW_NAME:
			ev.Op = fsnotify.Create
		case windows.FILE_ACTION_REMOVED:
			ev.Op = fsnotify.Remove
		case windows.FILE_ACTION_RENAMED_OLD_NAME:
			ev.Op = fsnotify.Rename
		default:
			ev.Op = fsnotify.Write
		}
		b.send(ev)
		if info.NextEntryOffset == 0 {
			return
		}
		off += int(info.NextEntryOffset)
	}
}

func (b *recursiveBackend) send(ev fsnotify.Event) {
	b.mu.Lock()
	closed := b.closed
	b.mu.Unlock()
	if closed {
		return
	}
	select {
	case b.events <- ev:
	default:
		b.sendError(errors.New("watch: event queue full (events lost)"))
	}
}

func (b *recursiveBackend) sendError(err error) {
	select {
	case b.errors <- err:
	default:
	}
}

func (b *recursiveBackend) Remove(path string) error {
	path = filepath.Clean(path)
	b.mu.Lock()
	w, ok := b.watches[path]
	delete(b.watches, path)
	b.mu.Unlock()
	if !ok {
		return nil
	}
	b.stop(w)
	return nil
}

func (b *recursiveBackend) stop(w *dirWatch) {
	windows.SetEvent(w.stopEv)
	<-w.done
}

func (b *recursiveBackend) WatchList() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]string, 0, len(b.watches))
	for p := range b.watches {
		out = append(out, p)
	}
	return out
}

func (b *recursiveBackend) Events() <-chan fsnotify.Event { return b.events }
func (b *recursiveBackend) Errors() <-chan error          { return b.errors }
func (b *recursiveBackend) Recursive() bool               { return true }

func (b *recursiveBackend) Close() error {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return nil
	}
	b.closed = true
	ws := b.watches
	b.watches = map[string]*dirWatch{}
	b.mu.Unlock()
	for _, w := range ws {
		b.stop(w)
	}
	return nil
}
