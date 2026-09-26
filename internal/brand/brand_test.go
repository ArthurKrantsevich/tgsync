package brand

import (
	"bytes"
	"image/png"
	"testing"
	"unicode/utf8"
)

func TestTextsFitTelegramLimits(t *testing.T) {
	if n := utf8.RuneCountInString(ShortDescription); n == 0 || n > 120 {
		t.Fatalf("short description: %d runes", n)
	}
	if n := utf8.RuneCountInString(Description); n == 0 || n > 512 {
		t.Fatalf("description: %d runes", n)
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
