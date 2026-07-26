package secret

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
)

func TestBufferCopiesInputAndReads(t *testing.T) {
	t.Parallel()

	input := []byte("test-secret-alpha")
	buffer := New(input)
	input[0] = 'X'

	first := buffer.Bytes()
	if got := string(first); got != "test-secret-alpha" {
		t.Fatalf("Bytes() after caller mutation = %q", got)
	}
	first[0] = 'Y'
	if got := string(buffer.Bytes()); got != "test-secret-alpha" {
		t.Fatalf("Bytes() shared returned storage: %q", got)
	}
	if buffer.Len() != len("test-secret-alpha") || buffer.IsWiped() {
		t.Fatalf("new buffer state: len=%d wiped=%v", buffer.Len(), buffer.IsWiped())
	}
}

func TestBufferWipeZerosStorageAndIsIdempotent(t *testing.T) {
	t.Parallel()

	buffer := New([]byte("test-secret-to-wipe"))
	backing := buffer.state.data

	buffer.Wipe()
	buffer.Wipe()

	if !buffer.IsWiped() || buffer.Len() != 0 || len(buffer.Bytes()) != 0 {
		t.Fatalf("wiped buffer state: len=%d wiped=%v bytes=%q", buffer.Len(), buffer.IsWiped(), buffer.Bytes())
	}
	for i, value := range backing {
		if value != 0 {
			t.Fatalf("backing byte %d was not zeroed: %d", i, value)
		}
	}

	var zero Buffer
	zero.Wipe()
	zero.Wipe()
	if !zero.IsWiped() || zero.Len() != 0 || zero.Bytes() != nil {
		t.Fatalf("zero-value buffer is not safely wiped: len=%d wiped=%v bytes=%v", zero.Len(), zero.IsWiped(), zero.Bytes())
	}
}

func TestBufferNeverFormatsOrSerializesItsSecret(t *testing.T) {
	t.Parallel()

	const raw = "test-secret-format"
	buffer := New([]byte(raw))

	formatted := []string{
		buffer.String(),
		buffer.GoString(),
		fmt.Sprint(buffer),
		fmt.Sprintf("%v", buffer),
		fmt.Sprintf("%+v", buffer),
		fmt.Sprintf("%#v", buffer),
		fmt.Sprintf("%s", buffer),
		fmt.Sprintf("%q", buffer),
	}
	for _, got := range formatted {
		if strings.Contains(got, raw) || !strings.Contains(got, Redacted) {
			t.Errorf("unsafe formatted value %q", got)
		}
	}
	for _, format := range []string{"%x", "%d", "%p"} {
		if got := fmt.Sprintf(format, buffer); strings.Contains(got, raw) {
			t.Errorf("format %q leaked secret in %q", format, got)
		}
	}

	for _, value := range []any{buffer, *buffer, struct {
		Credential *Buffer `json:"credential"`
	}{Credential: buffer}} {
		encoded, err := json.Marshal(value)
		if err != nil {
			t.Fatalf("json.Marshal(%T) error = %v", value, err)
		}
		if strings.Contains(string(encoded), raw) || !strings.Contains(string(encoded), Redacted) {
			t.Errorf("unsafe JSON for %T: %s", value, encoded)
		}
	}

	text, err := buffer.MarshalText()
	if err != nil {
		t.Fatalf("MarshalText() error = %v", err)
	}
	if strings.Contains(string(text), raw) || string(text) != Redacted {
		t.Errorf("unsafe text serialization: %s", text)
	}
}

func TestBufferConcurrentReadAndWipe(t *testing.T) {
	buffer := New([]byte("test-secret-concurrent"))

	var workers sync.WaitGroup
	for range 32 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for range 100 {
				_ = buffer.Bytes()
				_ = buffer.Len()
				_ = buffer.IsWiped()
				_, _ = json.Marshal(buffer)
			}
		}()
	}
	buffer.Wipe()
	workers.Wait()

	if !buffer.IsWiped() || len(buffer.Bytes()) != 0 {
		t.Fatalf("buffer retained data after concurrent wipe: %q", buffer.Bytes())
	}
}
