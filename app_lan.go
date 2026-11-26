package main

import (
	"context"
	"crypto/rand"
	"fmt"
	"log"
	"sync"

	"modelkeeper/internal/lanview"
	"modelkeeper/internal/msg"
	"modelkeeper/internal/settings"
)

// DefaultLANPort is the port of the read-only view for other devices.
const DefaultLANPort = 8642

// LANStatus is shown in the settings.
type LANStatus struct {
	Enabled bool     `json:"enabled"`
	Running bool     `json:"running"`
	Port    int      `json:"port"`
	Links   []string `json:"links"` // one per network address of this PC, with the key
	Error   *msg.Msg `json:"error,omitempty"`
}

type lanState struct {
	mu     sync.Mutex
	cancel context.CancelFunc
	err    *msg.Msg
}

var lan lanState

// newLANKey returns a random access key (10 characters, easy to type).
func newLANKey() string {
	const alphabet = "abcdefghjkmnpqrstuvwxyz23456789"
	b := make([]byte, 10)
	rand.Read(b)
	for i := range b {
		b[i] = alphabet[int(b[i])%len(alphabet)]
	}
	return string(b)
}

// startLAN starts or stops the view according to the settings.
func (a *App) startLAN() {
	lan.mu.Lock()
	defer lan.mu.Unlock()
	if lan.cancel != nil {
		lan.cancel()
		lan.cancel = nil
	}
	lan.err = nil
	st := a.settings.Get()
	if !st.LANView || a.db == nil {
		return
	}
	if st.LANKey == "" {
		key := newLANKey()
		if a.settings.Update(func(s *settings.Settings) { s.LANKey = key }) != nil {
			return
		}
		st.LANKey = key
	}
	port := st.LANPort
	if port <= 0 {
		port = DefaultLANPort
	}
	ctx, cancel := context.WithCancel(context.Background())
	srv := &lanview.Server{DB: a.db, ThumbDir: a.layout.Thumbnails, Key: st.LANKey, Language: st.Language}
	if _, done, err := lanview.Listen(ctx, fmt.Sprintf(":%d", port), srv); err != nil {
		cancel()
		m := msg.New("lan.listenFailed", "port", port, "detail", err.Error())
		lan.err = &m
		log.Printf("lan view: %v", err)
		return
	} else {
		go func() {
			if err := <-done; err != nil {
				log.Printf("lan view stopped: %v", err)
			}
		}()
	}
	go func() {
		<-a.stop
		cancel()
	}()
	lan.cancel = cancel
	log.Printf("lan view on port %d", port)
}

// GetLANStatus returns the state of the read-only view for other devices.
func (a *App) GetLANStatus() (LANStatus, error) {
	if err := a.ready(); err != nil {
		return LANStatus{}, err
	}
	st := a.settings.Get()
	port := st.LANPort
	if port <= 0 {
		port = DefaultLANPort
	}
	lan.mu.Lock()
	s := LANStatus{Enabled: st.LANView, Running: lan.cancel != nil, Port: port, Error: lan.err, Links: []string{}}
	lan.mu.Unlock()
	if s.Running {
		for _, ip := range lanview.Addresses() {
			s.Links = append(s.Links, fmt.Sprintf("http://%s:%d/?k=%s", ip, port, st.LANKey))
		}
	}
	return s, nil
}

// SetLANView turns the read-only view on or off (port 0 = default).
func (a *App) SetLANView(on bool, port int) (LANStatus, error) {
	if err := a.ready(); err != nil {
		return LANStatus{}, err
	}
	if port != 0 && (port < 1024 || port > 65535) {
		return LANStatus{}, msg.Wrap(nil, "lan.badPort")
	}
	if err := a.settings.Update(func(s *settings.Settings) {
		s.LANView, s.LANPort = on, port
		if s.LANKey == "" {
			s.LANKey = newLANKey()
		}
	}); err != nil {
		return LANStatus{}, msg.Wrap(err, "error.settingsSave")
	}
	a.startLAN()
	return a.GetLANStatus()
}

// NewLANKey replaces the access key: links given out before stop working.
func (a *App) NewLANKey() (LANStatus, error) {
	if err := a.ready(); err != nil {
		return LANStatus{}, err
	}
	if err := a.settings.Update(func(s *settings.Settings) { s.LANKey = newLANKey() }); err != nil {
		return LANStatus{}, msg.Wrap(err, "error.settingsSave")
	}
	a.startLAN()
	return a.GetLANStatus()
}
