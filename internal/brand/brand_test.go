package brand

import (
	"bytes"
	"image/png"
	"testing"
	"unicode/utf8"

	"github.com/ArthurKrantsevich/tgsync/internal/i18n"
)

func TestTextsFitTelegramLimits(t *testing.T) {
	defer i18n.Set(i18n.Current())
	for _, l := range []i18n.Lang{i18n.EN, i18n.RU} {
		i18n.Set(l)
		if n := utf8.RuneCountInString(ShortDescription()); n == 0 || n > 120 {
			t.Fatalf("%s short description: %d runes", l, n)
		}
		if n := utf8.RuneCountInString(Description()); n == 0 || n > 512 {
			t.Fatalf("%s description: %d runes", l, n)
		}
		if n := utf8.RuneCountInString(GroupDescription()); n == 0 || n > 255 {
			t.Fatalf("%s group description: %d runes", l, n)
		}
	}
}

func TestAvatar(t *testing.T) {
	img, err := png.Decode(bytes.NewReader(Avatar))
	if err != nil {
		t.Fatal(err)
	}
	if b := img.Bounds(); b.Dx() != 640 || b.Dy() != 640 {
		t.Fatalf("size: %v", b)
	}
}
