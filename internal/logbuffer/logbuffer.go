package logbuffer

import (
	"io"
	"sync"
	"time"
)

// Entry is a single log line with a timestamp.
type Entry struct {
	Time    time.Time `json:"time"`
	Message string    `json:"message"`
}

// Buffer is a thread-safe ring buffer that captures log output.
// It implements io.Writer so it can be used with log.SetOutput.
type Buffer struct {
	mu      sync.RWMutex
	entries []Entry
	size    int
	pos     int // next write position (wraps around)
	total   int // total entries ever written (for sequencing)

	// subscribers receive new entries in real time
	subs   map[int]chan Entry
	nextID int
}

// New creates a Buffer that retains the last `size` log entries.
func New(size int) *Buffer {
	return &Buffer{
		entries: make([]Entry, size),
		size:    size,
		subs:    make(map[int]chan Entry),
	}
}

// Write implements io.Writer. Each call is treated as one log entry.
// Newlines are stripped from the end.
func (b *Buffer) Write(p []byte) (int, error) {
	msg := string(p)
	if len(msg) > 0 && msg[len(msg)-1] == '\n' {
		msg = msg[:len(msg)-1]
	}

	entry := Entry{Time: time.Now(), Message: msg}

	b.mu.Lock()
	b.entries[b.pos] = entry
	b.pos = (b.pos + 1) % b.size
	b.total++

	// Fan out to subscribers (non-blocking).
	for _, ch := range b.subs {
		select {
		case ch <- entry:
		default:
		}
	}
	b.mu.Unlock()

	return len(p), nil
}

// Entries returns all buffered entries in chronological order.
func (b *Buffer) Entries() []Entry {
	b.mu.RLock()
	defer b.mu.RUnlock()

	if b.total == 0 {
		return nil
	}

	count := b.total
	if count > b.size {
		count = b.size
	}

	result := make([]Entry, 0, count)
	start := b.pos - count
	if start < 0 {
		start += b.size
	}
	for i := 0; i < count; i++ {
		idx := (start + i) % b.size
		result = append(result, b.entries[idx])
	}
	return result
}

// Subscribe returns a channel that receives new entries as they are written.
// Call Unsubscribe with the returned ID when done.
func (b *Buffer) Subscribe() (int, <-chan Entry) {
	b.mu.Lock()
	defer b.mu.Unlock()
	id := b.nextID
	b.nextID++
	ch := make(chan Entry, 64)
	b.subs[id] = ch
	return id, ch
}

// Unsubscribe removes a subscriber and closes its channel.
func (b *Buffer) Unsubscribe(id int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if ch, ok := b.subs[id]; ok {
		close(ch)
		delete(b.subs, id)
	}
}

// MultiWriter returns an io.Writer that writes to both the buffer
// and the provided writer (typically os.Stderr for console output).
func (b *Buffer) MultiWriter(w io.Writer) io.Writer {
	return io.MultiWriter(w, b)
}
