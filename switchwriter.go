// Package switchwriter provides a thread-safe, switchable io.Writer implementation.
//
// SwitchWriter allows clients to write to a single stable io.Writer instance while
// the underlying destination can be atomically swapped at runtime. This is useful
// for scenarios like log rotation by size or time, where callers should never need
// to know that rotation is happening.
//
// # Concurrency Guarantees
//
// SwitchWriter is safe for concurrent use by multiple goroutines. All methods
// are synchronized to prevent data races, loss, or interleaving during switches.
//
// # Basic Usage
//
//	sw := switchwriter.New(file1)
//	log.SetOutput(sw)
//
//	// Later, without touching loggers
//	sw.Switch(file2)
//
// # Automatic Rotation
//
//	sw := switchwriter.NewWithRotation(switchwriter.RotationConfig{
//	    MaxSizeBytes: 100 << 20,        // 100 MB
//	    RotateEvery:  24 * time.Hour,   // Daily
//	    OpenNew:      openLogFile,
//	})
package switchwriter

import (
	"errors"
	"io"
	"sync"
	"time"
)

// ErrNilWriter is returned when attempting to create or switch to a nil writer.
var ErrNilWriter = errors.New("switchwriter: writer cannot be nil")

// ErrNilOpenNew is returned when rotation is configured but OpenNew is nil.
var ErrNilOpenNew = errors.New("switchwriter: OpenNew function cannot be nil when rotation is enabled")

// ErrClosed is returned when writing to a closed SwitchWriter.
var ErrClosed = errors.New("switchwriter: writer is closed")

// RotationConfig defines the configuration for automatic rotation.
type RotationConfig struct {
	// MaxSizeBytes triggers rotation when the total bytes written exceeds this value.
	// Set to 0 to disable size-based rotation.
	MaxSizeBytes int64

	// RotateEvery triggers rotation on a fixed time interval.
	// Set to 0 to disable time-based rotation.
	RotateEvery time.Duration

	// OpenNew is called to create a new writer when rotation occurs.
	// This function must be provided if any rotation is enabled.
	OpenNew func() (io.Writer, error)

	// OnRotateError is an optional callback invoked when rotation fails.
	// If rotation fails, writing continues to the previous writer.
	OnRotateError func(err error)

	// OnClose is an optional callback invoked when an old writer is closed.
	// This can be used to handle close errors or perform cleanup.
	OnClose func(oldWriter io.Writer, err error)
}

// SwitchWriter implements io.Writer with atomic switching capability.
// It is safe for concurrent use by multiple goroutines.
type SwitchWriter struct {
	mu           sync.Mutex
	writer       io.Writer
	closed       bool
	bytesWritten int64
	lastRotation time.Time
	config       *RotationConfig
	stopTimer    chan struct{}
	timerStopped chan struct{}
}

// New creates a new SwitchWriter with the given initial writer.
// The initial writer must not be nil.
func New(initial io.Writer) (*SwitchWriter, error) {
	if initial == nil {
		return nil, ErrNilWriter
	}
	return &SwitchWriter{
		writer:       initial,
		lastRotation: time.Now(),
	}, nil
}

// MustNew creates a new SwitchWriter with the given initial writer.
// It panics if initial is nil.
func MustNew(initial io.Writer) *SwitchWriter {
	sw, err := New(initial)
	if err != nil {
		panic(err)
	}
	return sw
}

// NewWithRotation creates a new SwitchWriter with automatic rotation support.
// The OpenNew function in config must be provided to create new writers during rotation.
func NewWithRotation(config RotationConfig) (*SwitchWriter, error) {
	if config.OpenNew == nil {
		return nil, ErrNilOpenNew
	}

	initial, err := config.OpenNew()
	if err != nil {
		return nil, err
	}

	sw := &SwitchWriter{
		writer:       initial,
		lastRotation: time.Now(),
		config:       &config,
	}

	// Start time-based rotation timer if configured
	if config.RotateEvery > 0 {
		sw.stopTimer = make(chan struct{})
		sw.timerStopped = make(chan struct{})
		go sw.rotationTimer()
	}

	return sw, nil
}

// Write writes p to the underlying writer.
// It implements io.Writer interface.
//
// If automatic rotation is configured and thresholds are exceeded,
// rotation occurs before the write. If rotation fails, writing
// continues to the previous writer.
func (sw *SwitchWriter) Write(p []byte) (int, error) {
	sw.mu.Lock()
	defer sw.mu.Unlock()

	if sw.closed {
		return 0, ErrClosed
	}

	// Check if size-based rotation is needed
	if sw.config != nil && sw.config.MaxSizeBytes > 0 {
		if sw.bytesWritten+int64(len(p)) > sw.config.MaxSizeBytes {
			sw.rotateLocked()
		}
	}

	n, err := sw.writer.Write(p)
	sw.bytesWritten += int64(n)
	return n, err
}

// Switch atomically replaces the underlying writer with newWriter.
// If the previous writer implements io.Closer, it will be closed after the switch.
// The newWriter must not be nil.
func (sw *SwitchWriter) Switch(newWriter io.Writer) error {
	if newWriter == nil {
		return ErrNilWriter
	}

	sw.mu.Lock()
	defer sw.mu.Unlock()

	if sw.closed {
		return ErrClosed
	}

	oldWriter := sw.writer
	sw.writer = newWriter
	sw.bytesWritten = 0
	sw.lastRotation = time.Now()

	// Close old writer if it implements io.Closer
	sw.closeOldWriter(oldWriter)

	return nil
}

// Close closes the SwitchWriter and the underlying writer if it implements io.Closer.
// After Close, all Write calls will return ErrClosed.
func (sw *SwitchWriter) Close() error {
	sw.mu.Lock()
	defer sw.mu.Unlock()

	if sw.closed {
		return nil
	}

	sw.closed = true

	// Stop the rotation timer if running
	if sw.stopTimer != nil {
		close(sw.stopTimer)
		sw.mu.Unlock()
		<-sw.timerStopped
		sw.mu.Lock()
	}

	// Close the underlying writer if it implements io.Closer
	if closer, ok := sw.writer.(io.Closer); ok {
		return closer.Close()
	}

	return nil
}

// BytesWritten returns the number of bytes written since the last rotation.
func (sw *SwitchWriter) BytesWritten() int64 {
	sw.mu.Lock()
	defer sw.mu.Unlock()
	return sw.bytesWritten
}

// LastRotation returns the time of the last rotation or initial creation.
func (sw *SwitchWriter) LastRotation() time.Time {
	sw.mu.Lock()
	defer sw.mu.Unlock()
	return sw.lastRotation
}

// rotateLocked performs rotation while holding the lock.
// It must be called with sw.mu held.
func (sw *SwitchWriter) rotateLocked() {
	if sw.config == nil || sw.config.OpenNew == nil {
		return
	}

	newWriter, err := sw.config.OpenNew()
	if err != nil {
		if sw.config.OnRotateError != nil {
			sw.config.OnRotateError(err)
		}
		// Continue with the current writer
		return
	}

	oldWriter := sw.writer
	sw.writer = newWriter
	sw.bytesWritten = 0
	sw.lastRotation = time.Now()

	sw.closeOldWriter(oldWriter)
}

// closeOldWriter closes the old writer if it implements io.Closer.
func (sw *SwitchWriter) closeOldWriter(oldWriter io.Writer) {
	if closer, ok := oldWriter.(io.Closer); ok {
		err := closer.Close()
		if sw.config != nil && sw.config.OnClose != nil {
			sw.config.OnClose(oldWriter, err)
		}
	}
}

// rotationTimer handles time-based rotation in a separate goroutine.
func (sw *SwitchWriter) rotationTimer() {
	defer close(sw.timerStopped)

	ticker := time.NewTicker(sw.config.RotateEvery)
	defer ticker.Stop()

	for {
		select {
		case <-sw.stopTimer:
			return
		case <-ticker.C:
			sw.mu.Lock()
			if !sw.closed {
				sw.rotateLocked()
			}
			sw.mu.Unlock()
		}
	}
}
