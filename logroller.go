package logrotate

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Rotator implements io.Writer and handles file rotation based on time and/or size.
// It is safe for concurrent use.
// Usage example:
//    opts := logrotate.Options{
//        Filename:       "app.log",
//        RotationTime:   time.Hour,
//        MaxSize:        10 * 1024 * 1024, // 10 MB
//    }
//    w, err := logrotate.New(opts)
//    if err != nil {
//        panic(err)
//    }
//    logger := log.New(w, "", log.LstdFlags)

// Options configures the Rotator behavior.
type Options struct {
	// Filename is the base file path for logs. Rotated files will have a timestamp suffix.
	Filename string
	// RotationTime defines an interval for time-based rotation. Zero disables time rotation.
	// Examples: time.Minute, time.Hour, 24*time.Hour, 7*24*time.Hour
	RotationTime time.Duration
	// MaxSize defines a byte-size threshold for size-based rotation. Zero disables size rotation.
	MaxSize int64
}

// Rotator writes logs to a file and rotates according to Options.
type Rotator struct {
	opts       Options
	file       *os.File
	size       int64
	mu         sync.Mutex
	ticker     *time.Ticker
	quitTicker chan struct{}
}

// New creates a Rotator based on opts. It returns an io.Writer.
func New(opts Options) (*Rotator, error) {
	if opts.Filename == "" {
		return nil, fmt.Errorf("logrotate: Filename is required")
	}

	r := &Rotator{
		opts:       opts,
		quitTicker: make(chan struct{}),
	}

	// ensure directory exists
	dir := filepath.Dir(r.opts.Filename)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, err
	}

	if err := r.openNew(); err != nil {
		return nil, err
	}

	// start time-based rotation if requested
	if opts.RotationTime > 0 {
		r.ticker = time.NewTicker(opts.RotationTime)
		go func() {
			for {
				select {
				case <-r.ticker.C:
					r.rotate()
				case <-r.quitTicker:
					r.ticker.Stop()
					return
				}
			}
		}()
	}

	return r, nil
}

// Write writes data to the current log file, rotating if thresholds are reached.
func (r *Rotator) Write(p []byte) (n int, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	// write
	n, err = r.file.Write(p)
	if err != nil {
		return n, err
	}
	r.size += int64(n)

	// rotate on size
	if r.opts.MaxSize > 0 && r.size >= r.opts.MaxSize {
		err = r.rotate()
	}
	return n, err
}

// Close stops the Rotator, closes file and ticker.
func (r *Rotator) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.ticker != nil {
		close(r.quitTicker)
	}
	return r.file.Close()
}

// rotate closes the current file, renames it with timestamp suffix, and opens a new file.
func (r *Rotator) rotate() error {
	// close current
	r.file.Close()

	// rename
	timestamp := time.Now().Format("20060102-150405")
	newName := fmt.Sprintf("%s.%s", r.opts.Filename, timestamp)
	if err := os.Rename(r.opts.Filename, newName); err != nil && !os.IsNotExist(err) {
		return err
	}

	// open new
	err := r.openNew()
	return err
}

// openNew opens a fresh log file and resets size counter.
func (r *Rotator) openNew() error {
	f, err := os.OpenFile(r.opts.Filename, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}

	r.file = f
	info, err := f.Stat()
	if err != nil {
		return err
	}
	r.size = info.Size()
	return nil
}
