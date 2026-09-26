package files

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRead(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.md"), []byte("# A"), 0o644); err != nil {
		t.Fatal(err)
	}
	rel, data, err := Read(dir, "a.md", nil)
	if err != nil || rel != "a.md" || string(data) != "# A" {
		t.Fatalf("Read = %q, %q, %v", rel, data, err)
	}
	if _, _, err := Read(dir, "missing.md", nil); err == nil {
		t.Fatal("a missing file must fail")
	}
}

func TestReadRefusesFileSwappedAfterCheck(t *testing.T) {
	dir := t.TempDir()
	for name, body := range map[string]string{"a.md": "public", "b.md": "other"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	c, err := resolve(dir, "a.md", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(dir, "b.md"), filepath.Join(dir, "a.md")); err != nil {
		t.Fatal(err)
	}
	if data, err := readChecked(c.real, c.info, MaxSize); err == nil {
		t.Fatalf("a file replaced after the check was read: %q", data)
	}
}

func TestReadRefusesSymlinkSwappedAfterCheck(t *testing.T) {
	dir, outside := t.TempDir(), t.TempDir()
	secret := filepath.Join(outside, ".env")
	if err := os.WriteFile(secret, []byte("TOKEN=x"), 0o600); err != nil {
		t.Fatal(err)
	}
	a := filepath.Join(dir, "a.md")
	if err := os.WriteFile(a, []byte("public"), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := resolve(dir, "a.md", nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = os.Remove(a)
	if err := os.Symlink(secret, a); err != nil {
		t.Skip("symlink:", err)
	}
	if data, err := readChecked(c.real, c.info, MaxSize); err == nil || strings.Contains(string(data), "TOKEN") {
		t.Fatalf("a symlink swapped in after the check was followed: %q %v", data, err)
	}
}

func TestReadLimit(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "big.txt"), []byte("0123456789"), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := resolve(dir, "big.txt", nil)
	if err != nil {
		t.Fatal(err)
	}
	// The file grew past the limit after Resolve checked its size.
	if _, err := readChecked(c.real, c.info, 4); err == nil {
		t.Fatal("a file over the limit must be refused")
	}
	if data, err := readChecked(c.real, c.info, 10); err != nil || len(data) != 10 {
		t.Fatalf("a file at the limit: %d bytes, %v", len(data), err)
	}
}
