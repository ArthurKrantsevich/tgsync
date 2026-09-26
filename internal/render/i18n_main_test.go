package render

import (
	"os"
	"testing"

	"github.com/ArthurKrantsevich/tgsync/internal/i18n"
)

// TestMain pins Russian: the existing tests assert the Russian texts.
func TestMain(m *testing.M) {
	i18n.Set(i18n.RU)
	os.Exit(m.Run())
}
