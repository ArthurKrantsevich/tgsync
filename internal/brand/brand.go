// Package brand holds the bot's profile: avatar and descriptions, applied
// with `tgsync profile`. Regenerate the avatar with scripts/avatar.py.
package brand

import (
	_ "embed"

	"github.com/ArthurKrantsevich/tgsync/internal/i18n"
)

//go:embed avatar.png
var Avatar []byte

// ShortDescription is shown on the bot's profile page (up to 120 characters).
func ShortDescription() string { return i18n.T("brand.short") }

// Description is shown in an empty chat with the bot (up to 512 characters).
func Description() string { return i18n.T("brand.description") }

// GroupDescription is set on the group when it has none (up to 255 characters).
func GroupDescription() string { return i18n.T("brand.group") }
