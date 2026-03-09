package logging

import (
	"fmt"
	"log"
	"os"
)

// Init sets up the standard logger to write to path.
func Init(path string) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("failed to open log file %q: %w", path, err)
	}
	log.SetOutput(f)
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)
	return nil
}

// Discard silences all log output (default when no --log flag is given).
func Discard() {
	log.SetOutput(os.Stderr)
	log.SetFlags(0)
	// Redirect to discard
	log.SetOutput(nopWriter{})
}

type nopWriter struct{}

func (nopWriter) Write(p []byte) (int, error) { return len(p), nil }
