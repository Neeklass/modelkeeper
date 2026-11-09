// Package msg carries translatable messages from the backend to the
// frontend: a message key plus parameters, never a finished sentence (N4).
package msg

import (
	"encoding/json"
	"errors"
)

// Msg is a translation key with optional interpolation parameters.
type Msg struct {
	Key    string         `json:"key"`
	Params map[string]any `json:"params,omitempty"`
}

// New creates a message. kv is a flat list of alternating keys and values.
func New(key string, kv ...any) Msg {
	m := Msg{Key: key}
	if len(kv) > 0 {
		m.Params = make(map[string]any, len(kv)/2)
		for i := 0; i+1 < len(kv); i += 2 {
			if k, ok := kv[i].(string); ok {
				m.Params[k] = kv[i+1]
			}
		}
	}
	return m
}

// Error is an error that carries a translatable message. Its Error() string
// is JSON so that it survives the Wails bridge (which rejects JS promises
// with err.Error()) and can be decoded by the frontend.
type Error struct {
	Msg
	Cause error `json:"-"`
}

// Wrap wraps cause with a message key. The cause's text is attached as
// the "detail" parameter so it is still visible for diagnostics.
func Wrap(cause error, key string, kv ...any) *Error {
	m := New(key, kv...)
	if cause != nil {
		if m.Params == nil {
			m.Params = map[string]any{}
		}
		m.Params["detail"] = cause.Error()
	}
	return &Error{Msg: m, Cause: cause}
}

func (e *Error) Error() string {
	b, _ := json.Marshal(e.Msg)
	return string(b)
}

func (e *Error) Unwrap() error { return e.Cause }

// From converts any error into a Msg; unknown errors map to "error.unknown".
func From(err error) Msg {
	var me *Error
	if errors.As(err, &me) {
		return me.Msg
	}
	return New("error.unknown", "detail", err.Error())
}
