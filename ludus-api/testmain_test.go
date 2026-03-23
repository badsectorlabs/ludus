package ludusapi

import (
	"io"
	"log/slog"
	"os"
	"testing"
)

func TestMain(m *testing.M) {
	// Several production functions read ServerConfiguration.DatabaseEncryptionKey
	// for AES-256-GCM encryption — it must be exactly 32 bytes.
	ServerConfiguration.DatabaseEncryptionKey = "test-key-that-is-32-chars-long!!"

	// Functions like validateBytes call logger.Debug() — a nil logger panics.
	logger = slog.New(slog.NewTextHandler(io.Discard, nil))

	os.Exit(m.Run())
}
