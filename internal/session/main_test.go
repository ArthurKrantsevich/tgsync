package session

import (
	"os"
	"testing"

	"github.com/ArthurKrantsevich/tgsync/internal/files"
)

func TestMain(m *testing.M) {
	// Snapshot side indexes of the test repos go to a temporary cache.
	cache, err := os.MkdirTemp("", "tgsync-cache-")
	if err != nil {
		panic(err)
	}
	files.CacheDir = func() (string, error) { return cache, nil }
	code := m.Run()
	os.RemoveAll(cache)
	os.Exit(code)
}
