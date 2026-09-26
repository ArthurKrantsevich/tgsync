package session

import "github.com/ArthurKrantsevich/tgsync/internal/i18n"

// The package already has a TestMain (main_test.go); the tests assert the
// Russian texts, so the language is pinned here, before any test runs.
func init() { i18n.Set(i18n.RU) }
