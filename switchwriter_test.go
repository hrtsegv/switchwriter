package switchwriter

import (
	"bytes"
	"errors"
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// testWriter is a thread-safe bytes.Buffer for testing.
type testWriter struct {
	mu     sync.Mutex
	buf    bytes.Buffer
	closed bool
}

func (tw *testWriter) Write(p []byte) (int, error) {
	tw.mu.Lock()
	defer tw.mu.Unlock()
	if tw.closed {
		return 0, errors.New("writer is closed")
	}
	return tw.buf.Write(p)
}

func (tw *testWriter) Close() error {
	tw.mu.Lock()
	defer tw.mu.Unlock()
	tw.closed = true
	return nil
}

func (tw *testWriter) String() string {
	tw.mu.Lock()
	defer tw.mu.Unlock()
	return tw.buf.String()
}

func (tw *testWriter) Len() int {
	tw.mu.Lock()
	defer tw.mu.Unlock()
	return tw.buf.Len()
}

func (tw *testWriter) IsClosed() bool {
	tw.mu.Lock()
	defer tw.mu.Unlock()
	return tw.closed
}

// TestNew tests basic creation.
func TestNew(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		buf := &bytes.Buffer{}
		sw, err := New(buf)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if sw == nil {
			t.Fatal("expected non-nil SwitchWriter")
		}
	})

	t.Run("nil writer", func(t *testing.T) {
		sw, err := New(nil)
		if err != ErrNilWriter {
			t.Fatalf("expected ErrNilWriter, got: %v", err)
		}
		if sw != nil {
			t.Fatal("expected nil SwitchWriter")
		}
	})
}

// TestMustNew tests the panic version of New.
func TestMustNew(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		buf := &bytes.Buffer{}
		sw := MustNew(buf)
		if sw == nil {
			t.Fatal("expected non-nil SwitchWriter")
		}
	})

	t.Run("panic on nil", func(t *testing.T) {
		defer func() {
			if r := recover(); r == nil {
				t.Fatal("expected panic")
			}
		}()
		MustNew(nil)
	})
}

// TestWrite tests basic write operations.
func TestWrite(t *testing.T) {
	buf := &bytes.Buffer{}
	sw, _ := New(buf)

	data := []byte("hello world")
	n, err := sw.Write(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != len(data) {
		t.Fatalf("expected %d bytes written, got %d", len(data), n)
	}
	if buf.String() != "hello world" {
		t.Fatalf("expected 'hello world', got '%s'", buf.String())
	}
}

// TestWriteAfterClose tests that writes fail after closing.
func TestWriteAfterClose(t *testing.T) {
	buf := &bytes.Buffer{}
	sw, _ := New(buf)

	if err := sw.Close(); err != nil {
		t.Fatalf("unexpected error on close: %v", err)
	}

	_, err := sw.Write([]byte("test"))
	if err != ErrClosed {
		t.Fatalf("expected ErrClosed, got: %v", err)
	}
}

// TestSwitch tests basic switching functionality.
func TestSwitch(t *testing.T) {
	buf1 := &testWriter{}
	buf2 := &testWriter{}

	sw, _ := New(buf1)

	sw.Write([]byte("to buf1"))
	if err := sw.Switch(buf2); err != nil {
		t.Fatalf("unexpected error on switch: %v", err)
	}
	sw.Write([]byte("to buf2"))

	if buf1.String() != "to buf1" {
		t.Fatalf("expected 'to buf1', got '%s'", buf1.String())
	}
	if buf2.String() != "to buf2" {
		t.Fatalf("expected 'to buf2', got '%s'", buf2.String())
	}
}

// TestSwitchClosesOldWriter tests that old writers are closed after switch.
func TestSwitchClosesOldWriter(t *testing.T) {
	buf1 := &testWriter{}
	buf2 := &testWriter{}

	sw, _ := New(buf1)
	sw.Switch(buf2)

	if !buf1.IsClosed() {
		t.Fatal("expected old writer to be closed")
	}
	if buf2.IsClosed() {
		t.Fatal("new writer should not be closed")
	}
}

// TestSwitchNilWriter tests that switching to nil fails.
func TestSwitchNilWriter(t *testing.T) {
	buf := &bytes.Buffer{}
	sw, _ := New(buf)

	err := sw.Switch(nil)
	if err != ErrNilWriter {
		t.Fatalf("expected ErrNilWriter, got: %v", err)
	}
}

// TestSwitchAfterClose tests that switch fails after closing.
func TestSwitchAfterClose(t *testing.T) {
	buf := &bytes.Buffer{}
	sw, _ := New(buf)
	sw.Close()

	err := sw.Switch(&bytes.Buffer{})
	if err != ErrClosed {
		t.Fatalf("expected ErrClosed, got: %v", err)
	}
}

// TestClose tests close functionality.
func TestClose(t *testing.T) {
	t.Run("closes underlying writer", func(t *testing.T) {
		buf := &testWriter{}
		sw, _ := New(buf)
		sw.Close()

		if !buf.IsClosed() {
			t.Fatal("expected underlying writer to be closed")
		}
	})

	t.Run("multiple closes are safe", func(t *testing.T) {
		buf := &testWriter{}
		sw, _ := New(buf)
		sw.Close()
		err := sw.Close()
		if err != nil {
			t.Fatalf("second close should not error: %v", err)
		}
	})

	t.Run("non-closer writer", func(t *testing.T) {
		buf := &bytes.Buffer{} // doesn't implement io.Closer
		sw, _ := New(buf)
		err := sw.Close()
		if err != nil {
			t.Fatalf("close should succeed for non-closer: %v", err)
		}
	})
}

// TestBytesWritten tests the bytes counter.
func TestBytesWritten(t *testing.T) {
	buf := &bytes.Buffer{}
	sw, _ := New(buf)

	sw.Write([]byte("hello"))
	if sw.BytesWritten() != 5 {
		t.Fatalf("expected 5 bytes, got %d", sw.BytesWritten())
	}

	sw.Write([]byte(" world"))
	if sw.BytesWritten() != 11 {
		t.Fatalf("expected 11 bytes, got %d", sw.BytesWritten())
	}

	// After switch, counter should reset
	sw.Switch(&bytes.Buffer{})
	if sw.BytesWritten() != 0 {
		t.Fatalf("expected 0 bytes after switch, got %d", sw.BytesWritten())
	}
}

// TestConcurrentWrites tests concurrent write safety.
func TestConcurrentWrites(t *testing.T) {
	buf := &testWriter{}
	sw, _ := New(buf)

	var wg sync.WaitGroup
	numWriters := 100
	writesPerWriter := 100

	for i := 0; i < numWriters; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			data := []byte("x")
			for j := 0; j < writesPerWriter; j++ {
				sw.Write(data)
			}
		}(i)
	}

	wg.Wait()

	expected := numWriters * writesPerWriter
	if buf.Len() != expected {
		t.Fatalf("expected %d bytes, got %d", expected, buf.Len())
	}
}

// TestConcurrentWritesAndSwitch tests concurrent writes with switching.
func TestConcurrentWritesAndSwitch(t *testing.T) {
	buf1 := &testWriter{}
	sw, _ := New(buf1)

	var wg sync.WaitGroup
	var totalWrites atomic.Int64

	// Writers
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				n, err := sw.Write([]byte("x"))
				if err == nil {
					totalWrites.Add(int64(n))
				}
				time.Sleep(time.Microsecond)
			}
		}()
	}

	// Switchers
	var buffers []*testWriter
	buffers = append(buffers, buf1)
	var bufMu sync.Mutex

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 5; j++ {
				newBuf := &testWriter{}
				bufMu.Lock()
				buffers = append(buffers, newBuf)
				bufMu.Unlock()
				sw.Switch(newBuf)
				time.Sleep(time.Millisecond)
			}
		}()
	}

	wg.Wait()
	sw.Close()

	// Verify no data was lost
	var totalReceived int
	bufMu.Lock()
	for _, buf := range buffers {
		totalReceived += buf.Len()
	}
	bufMu.Unlock()

	if int64(totalReceived) != totalWrites.Load() {
		t.Fatalf("data loss detected: wrote %d, received %d", totalWrites.Load(), totalReceived)
	}
}

// TestNewWithRotation tests creation with rotation config.
func TestNewWithRotation(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		var writerCount int
		sw, err := NewWithRotation(RotationConfig{
			MaxSizeBytes: 100,
			OpenNew: func() (io.Writer, error) {
				writerCount++
				return &testWriter{}, nil
			},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if sw == nil {
			t.Fatal("expected non-nil SwitchWriter")
		}
		if writerCount != 1 {
			t.Fatalf("expected OpenNew to be called once, got %d", writerCount)
		}
		sw.Close()
	})

	t.Run("nil OpenNew", func(t *testing.T) {
		sw, err := NewWithRotation(RotationConfig{
			MaxSizeBytes: 100,
		})
		if err != ErrNilOpenNew {
			t.Fatalf("expected ErrNilOpenNew, got: %v", err)
		}
		if sw != nil {
			t.Fatal("expected nil SwitchWriter")
		}
	})

	t.Run("OpenNew error", func(t *testing.T) {
		expectedErr := errors.New("open failed")
		sw, err := NewWithRotation(RotationConfig{
			OpenNew: func() (io.Writer, error) {
				return nil, expectedErr
			},
		})
		if err != expectedErr {
			t.Fatalf("expected %v, got: %v", expectedErr, err)
		}
		if sw != nil {
			t.Fatal("expected nil SwitchWriter")
		}
	})
}

// TestSizeBasedRotation tests automatic size-based rotation.
func TestSizeBasedRotation(t *testing.T) {
	var writers []*testWriter
	var mu sync.Mutex

	sw, _ := NewWithRotation(RotationConfig{
		MaxSizeBytes: 10,
		OpenNew: func() (io.Writer, error) {
			w := &testWriter{}
			mu.Lock()
			writers = append(writers, w)
			mu.Unlock()
			return w, nil
		},
	})
	defer sw.Close()

	// Write 25 bytes in chunks that trigger rotation
	sw.Write([]byte("12345")) // 5 bytes
	sw.Write([]byte("67890")) // 10 bytes total
	sw.Write([]byte("abc"))   // would exceed 10, triggers rotation, then writes 3
	sw.Write([]byte("defgh")) // 8 bytes total in new writer
	sw.Write([]byte("ijk"))   // would exceed 10, triggers rotation, then writes 3

	mu.Lock()
	defer mu.Unlock()

	if len(writers) != 3 {
		t.Fatalf("expected 3 writers (rotations), got %d", len(writers))
	}

	// Verify data distribution
	if writers[0].String() != "1234567890" {
		t.Fatalf("writer 0: expected '1234567890', got '%s'", writers[0].String())
	}
	if writers[1].String() != "abcdefgh" {
		t.Fatalf("writer 1: expected 'abcdefgh', got '%s'", writers[1].String())
	}
	if writers[2].String() != "ijk" {
		t.Fatalf("writer 2: expected 'ijk', got '%s'", writers[2].String())
	}
}

// TestSizeBasedRotationResetsCounter tests that byte counter resets after rotation.
func TestSizeBasedRotationResetsCounter(t *testing.T) {
	sw, _ := NewWithRotation(RotationConfig{
		MaxSizeBytes: 10,
		OpenNew: func() (io.Writer, error) {
			return &testWriter{}, nil
		},
	})
	defer sw.Close()

	sw.Write([]byte("12345"))
	if sw.BytesWritten() != 5 {
		t.Fatalf("expected 5 bytes, got %d", sw.BytesWritten())
	}

	sw.Write([]byte("67890"))
	if sw.BytesWritten() != 10 {
		t.Fatalf("expected 10 bytes, got %d", sw.BytesWritten())
	}

	// This triggers rotation
	sw.Write([]byte("abc"))
	if sw.BytesWritten() != 3 {
		t.Fatalf("expected 3 bytes after rotation, got %d", sw.BytesWritten())
	}
}

// TestTimeBasedRotation tests automatic time-based rotation.
func TestTimeBasedRotation(t *testing.T) {
	var writerCount atomic.Int32

	sw, _ := NewWithRotation(RotationConfig{
		RotateEvery: 50 * time.Millisecond,
		OpenNew: func() (io.Writer, error) {
			writerCount.Add(1)
			return &testWriter{}, nil
		},
	})

	// Initial writer
	if writerCount.Load() != 1 {
		t.Fatalf("expected 1 initial writer, got %d", writerCount.Load())
	}

	// Wait for rotations
	time.Sleep(180 * time.Millisecond)

	sw.Close()

	// Should have rotated at least 2-3 times
	count := writerCount.Load()
	if count < 3 {
		t.Fatalf("expected at least 3 writers (initial + 2 rotations), got %d", count)
	}
}

// TestTimeBasedRotationStopsOnClose tests that timer stops on close.
func TestTimeBasedRotationStopsOnClose(t *testing.T) {
	var writerCount atomic.Int32

	sw, _ := NewWithRotation(RotationConfig{
		RotateEvery: 20 * time.Millisecond,
		OpenNew: func() (io.Writer, error) {
			writerCount.Add(1)
			return &testWriter{}, nil
		},
	})

	time.Sleep(50 * time.Millisecond)
	sw.Close()

	countAfterClose := writerCount.Load()
	time.Sleep(100 * time.Millisecond)

	if writerCount.Load() != countAfterClose {
		t.Fatal("rotation continued after close")
	}
}

// TestRotationError tests handling of rotation errors.
func TestRotationError(t *testing.T) {
	var callCount int
	var rotateErrors []error

	sw, _ := NewWithRotation(RotationConfig{
		MaxSizeBytes: 10,
		OpenNew: func() (io.Writer, error) {
			callCount++
			if callCount == 1 {
				return &testWriter{}, nil
			}
			return nil, errors.New("rotation failed")
		},
		OnRotateError: func(err error) {
			rotateErrors = append(rotateErrors, err)
		},
	})
	defer sw.Close()

	// First write goes to initial writer
	sw.Write([]byte("1234567890"))

	// This should trigger rotation, which fails, but write should continue
	n, err := sw.Write([]byte("abc"))
	if err != nil {
		t.Fatalf("write should succeed even if rotation fails: %v", err)
	}
	if n != 3 {
		t.Fatalf("expected 3 bytes written, got %d", n)
	}

	// Rotation error should have been reported
	if len(rotateErrors) != 1 {
		t.Fatalf("expected 1 rotation error, got %d", len(rotateErrors))
	}
}

// TestOnCloseCallback tests the OnClose callback.
func TestOnCloseCallback(t *testing.T) {
	var closedWriters []io.Writer

	sw, _ := NewWithRotation(RotationConfig{
		MaxSizeBytes: 10,
		OpenNew: func() (io.Writer, error) {
			return &testWriter{}, nil
		},
		OnClose: func(oldWriter io.Writer, err error) {
			closedWriters = append(closedWriters, oldWriter)
		},
	})

	sw.Write([]byte("1234567890"))
	sw.Write([]byte("a")) // triggers rotation

	if len(closedWriters) != 1 {
		t.Fatalf("expected 1 closed writer, got %d", len(closedWriters))
	}

	sw.Close()
}

// TestLastRotation tests the LastRotation method.
func TestLastRotation(t *testing.T) {
	sw, _ := NewWithRotation(RotationConfig{
		MaxSizeBytes: 10,
		OpenNew: func() (io.Writer, error) {
			return &testWriter{}, nil
		},
	})
	defer sw.Close()

	initial := sw.LastRotation()
	time.Sleep(10 * time.Millisecond)

	sw.Write([]byte("1234567890"))
	sw.Write([]byte("a")) // triggers rotation

	after := sw.LastRotation()
	if !after.After(initial) {
		t.Fatal("LastRotation should update after rotation")
	}
}

// TestConcurrentRotation tests concurrent writes with automatic rotation.
func TestConcurrentRotation(t *testing.T) {
	var totalReceived atomic.Int64

	sw, _ := NewWithRotation(RotationConfig{
		MaxSizeBytes: 100,
		OpenNew: func() (io.Writer, error) {
			return &countingWriter{total: &totalReceived}, nil
		},
	})

	var wg sync.WaitGroup
	var totalSent atomic.Int64

	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				data := []byte("test")
				n, _ := sw.Write(data)
				totalSent.Add(int64(n))
			}
		}()
	}

	wg.Wait()
	sw.Close()

	if totalSent.Load() != totalReceived.Load() {
		t.Fatalf("data loss: sent %d, received %d", totalSent.Load(), totalReceived.Load())
	}
}

// countingWriter counts bytes written across all instances.
type countingWriter struct {
	total *atomic.Int64
}

func (cw *countingWriter) Write(p []byte) (int, error) {
	cw.total.Add(int64(len(p)))
	return len(p), nil
}

func (cw *countingWriter) Close() error {
	return nil
}

// TestNoDataInterleaving tests that data is not interleaved during writes.
func TestNoDataInterleaving(t *testing.T) {
	var mu sync.Mutex
	var allData []byte

	sw, _ := New(&appendWriter{mu: &mu, data: &allData})

	var wg sync.WaitGroup
	messages := []string{"AAAA", "BBBB", "CCCC", "DDDD"}

	for _, msg := range messages {
		wg.Add(1)
		go func(m string) {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				sw.Write([]byte(m))
			}
		}(msg)
	}

	wg.Wait()

	// Verify no interleaving - each message should appear as complete unit
	mu.Lock()
	data := string(allData)
	mu.Unlock()

	for i := 0; i < len(data); i += 4 {
		chunk := data[i : i+4]
		if chunk != "AAAA" && chunk != "BBBB" && chunk != "CCCC" && chunk != "DDDD" {
			t.Fatalf("interleaved data detected at position %d: '%s'", i, chunk)
		}
	}
}

type appendWriter struct {
	mu   *sync.Mutex
	data *[]byte
}

func (aw *appendWriter) Write(p []byte) (int, error) {
	aw.mu.Lock()
	defer aw.mu.Unlock()
	*aw.data = append(*aw.data, p...)
	return len(p), nil
}

// TestWriterInterface verifies SwitchWriter implements io.Writer.
func TestWriterInterface(t *testing.T) {
	var _ io.Writer = (*SwitchWriter)(nil)
}

// TestWriteCloserInterface verifies SwitchWriter implements io.WriteCloser.
func TestWriteCloserInterface(t *testing.T) {
	var _ io.WriteCloser = (*SwitchWriter)(nil)
}
