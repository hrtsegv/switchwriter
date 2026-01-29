# switchwriter

A production-quality Go library that implements a **switchable `io.Writer`**. Clients write to a single stable `io.Writer` instance, while the underlying destination can be **atomically swapped at runtime** (e.g., for log rotation by size or time).

Callers never need to know that rotation is happening.

## Features

- **Thread-safe**: Safe for concurrent use by multiple goroutines
- **Hot-swappable**: Atomically replace the underlying writer at runtime
- **No data loss**: Writes are never lost or interleaved during switches
- **Automatic rotation**: Built-in support for size-based and time-based rotation
- **Lifecycle management**: Automatically closes old writers that implement `io.Closer`
- **Zero dependencies**: Uses only the Go standard library
- **Composable**: Works with any `io.Writer` implementation

## Installation

```bash
go get github.com/hrtsegv/switchwriter
```

## Usage

### Basic Usage

```go
package main

import (
    "log"
    "os"

    "github.com/hrtsegv/switchwriter"
)

func main() {
    file1, _ := os.Create("app.log")
    
    // Create a switchable writer
    sw, err := switchwriter.New(file1)
    if err != nil {
        log.Fatal(err)
    }
    defer sw.Close()
    
    // Use with standard logger
    log.SetOutput(sw)
    
    log.Println("Writing to file1")
    
    // Later, switch to a new file without touching the logger
    file2, _ := os.Create("app-rotated.log")
    sw.Switch(file2)
    
    log.Println("Now writing to file2")
}
```

### Using MustNew (panics on error)

```go
sw := switchwriter.MustNew(os.Stdout)
defer sw.Close()
```

### Size-Based Rotation

Automatically rotate when the file exceeds a certain size:

```go
sw, err := switchwriter.NewWithRotation(switchwriter.RotationConfig{
    MaxSizeBytes: 100 << 20, // 100 MB
    OpenNew: func() (io.Writer, error) {
        return os.Create(fmt.Sprintf("app-%d.log", time.Now().Unix()))
    },
})
if err != nil {
    log.Fatal(err)
}
defer sw.Close()

log.SetOutput(sw)
```

### Time-Based Rotation

Automatically rotate on a fixed interval:

```go
sw, err := switchwriter.NewWithRotation(switchwriter.RotationConfig{
    RotateEvery: 24 * time.Hour, // Daily rotation
    OpenNew: func() (io.Writer, error) {
        filename := fmt.Sprintf("app-%s.log", time.Now().Format("2006-01-02"))
        return os.Create(filename)
    },
})
if err != nil {
    log.Fatal(err)
}
defer sw.Close()

log.SetOutput(sw)
```

### Combined Size and Time Rotation

```go
sw, err := switchwriter.NewWithRotation(switchwriter.RotationConfig{
    MaxSizeBytes: 50 << 20,      // 50 MB
    RotateEvery:  1 * time.Hour, // Hourly
    OpenNew: func() (io.Writer, error) {
        return os.Create(fmt.Sprintf("app-%d.log", time.Now().UnixNano()))
    },
    OnRotateError: func(err error) {
        log.Printf("rotation failed: %v", err)
    },
    OnClose: func(oldWriter io.Writer, err error) {
        if err != nil {
            log.Printf("failed to close old writer: %v", err)
        }
    },
})
```

### Error Handling

```go
sw, err := switchwriter.NewWithRotation(switchwriter.RotationConfig{
    MaxSizeBytes: 10 << 20,
    OpenNew: func() (io.Writer, error) {
        return os.Create("app.log")
    },
    OnRotateError: func(err error) {
        // Log the error, alert monitoring, etc.
        // Writing continues to the previous writer
        log.Printf("WARNING: rotation failed: %v", err)
    },
})
```

## API Reference

### Types

#### SwitchWriter

```go
type SwitchWriter struct {
    // unexported fields
}
```

SwitchWriter implements `io.Writer` and `io.WriteCloser` with atomic switching capability.

#### RotationConfig

```go
type RotationConfig struct {
    // MaxSizeBytes triggers rotation when total bytes written exceeds this value.
    // Set to 0 to disable size-based rotation.
    MaxSizeBytes int64

    // RotateEvery triggers rotation on a fixed time interval.
    // Set to 0 to disable time-based rotation.
    RotateEvery time.Duration

    // OpenNew is called to create a new writer when rotation occurs.
    // Required if any rotation is enabled.
    OpenNew func() (io.Writer, error)

    // OnRotateError is an optional callback invoked when rotation fails.
    OnRotateError func(err error)

    // OnClose is an optional callback invoked when an old writer is closed.
    OnClose func(oldWriter io.Writer, err error)
}
```

### Functions

#### New

```go
func New(initial io.Writer) (*SwitchWriter, error)
```

Creates a new SwitchWriter with the given initial writer.

#### MustNew

```go
func MustNew(initial io.Writer) *SwitchWriter
```

Creates a new SwitchWriter, panicking if initial is nil.

#### NewWithRotation

```go
func NewWithRotation(config RotationConfig) (*SwitchWriter, error)
```

Creates a new SwitchWriter with automatic rotation support.

### Methods

#### Write

```go
func (sw *SwitchWriter) Write(p []byte) (int, error)
```

Writes p to the underlying writer. Thread-safe.

#### Switch

```go
func (sw *SwitchWriter) Switch(newWriter io.Writer) error
```

Atomically replaces the underlying writer. The old writer is closed if it implements `io.Closer`.

#### Close

```go
func (sw *SwitchWriter) Close() error
```

Closes the SwitchWriter and the underlying writer if it implements `io.Closer`.

#### BytesWritten

```go
func (sw *SwitchWriter) BytesWritten() int64
```

Returns the number of bytes written since the last rotation.

#### LastRotation

```go
func (sw *SwitchWriter) LastRotation() time.Time
```

Returns the time of the last rotation or initial creation.

## Concurrency Guarantees

- All methods are safe for concurrent use
- Writes are never lost during switches
- Data is never interleaved between writers
- Old writers receive no partial writes after switching
- No data corruption or duplication

## Performance

The implementation uses `sync.Mutex` for synchronization, providing:

- Fast writes with minimal lock contention
- Predictable latency under concurrent load
- No allocation overhead per write

For most use cases, the mutex-based approach provides excellent performance while maintaining simplicity and correctness.

## Testing

Run tests:

```bash
go test ./...
```

Run tests with race detector:

```bash
go test -race ./...
```

## License

MIT License
