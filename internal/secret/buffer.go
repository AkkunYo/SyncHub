package secret

import (
	"encoding/json"
	"runtime"
	"sync"
)

// Redacted is the placeholder emitted instead of secret material.
const Redacted = "[REDACTED]"

// Buffer owns a mutable copy of secret bytes that can be explicitly wiped.
// Its zero value is safe to use and represents an already-wiped buffer.
type Buffer struct {
	state *bufferState
}

type bufferState struct {
	mu    sync.RWMutex
	data  []byte
	wiped bool
}

// New constructs a Buffer without retaining the caller's byte slice.
func New(value []byte) *Buffer {
	return &Buffer{state: &bufferState{data: append([]byte(nil), value...)}}
}

// Bytes returns a copy of the currently held bytes.
func (b Buffer) Bytes() []byte {
	if b.state == nil {
		return nil
	}
	b.state.mu.RLock()
	defer b.state.mu.RUnlock()
	return append([]byte(nil), b.state.data...)
}

// Len returns the number of bytes currently held by the buffer.
func (b Buffer) Len() int {
	if b.state == nil {
		return 0
	}
	b.state.mu.RLock()
	defer b.state.mu.RUnlock()
	return len(b.state.data)
}

// IsWiped reports whether Wipe has been called. A zero-value Buffer is wiped.
func (b Buffer) IsWiped() bool {
	if b.state == nil {
		return true
	}
	b.state.mu.RLock()
	defer b.state.mu.RUnlock()
	return b.state.wiped
}

// Wipe clears the owned storage. Repeated and concurrent calls are safe.
func (b Buffer) Wipe() {
	if b.state == nil {
		return
	}
	b.state.mu.Lock()
	defer b.state.mu.Unlock()
	if b.state.wiped {
		return
	}

	data := b.state.data
	clear(data)
	runtime.KeepAlive(data)
	b.state.data = nil
	b.state.wiped = true
}

// String implements fmt.Stringer without exposing the buffer contents.
func (Buffer) String() string {
	return Redacted
}

// GoString implements fmt.GoStringer without exposing the buffer contents.
func (Buffer) GoString() string {
	return "secret.Buffer(" + Redacted + ")"
}

// MarshalJSON serializes only the redaction marker.
func (Buffer) MarshalJSON() ([]byte, error) {
	return json.Marshal(Redacted)
}

// MarshalText serializes only the redaction marker.
func (Buffer) MarshalText() ([]byte, error) {
	return []byte(Redacted), nil
}
