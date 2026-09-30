package projects

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestListAndDir(t *testing.T) {
	root := t.TempDir()
	for _, d := range []string{"beta", "alpha", ".hidden"} {
		if err := os.Mkdir(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	_ = os.WriteFile(filepath.Join(root, "file.txt"), nil, 0o644)
	r := Registry{Root: root}
	names, err := r.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(names) != 2 || names[0] != "alpha" || names[1] != "beta" {
		t.Fatalf("names: %v", names)
	}
	dir, err := r.Dir("alpha")
	if err != nil || dir != filepath.Join(root, "alpha") {
		t.Fatalf("dir=%q err=%v", dir, err)
	}
	if _, err := r.Dir("missing"); err == nil {
		t.Fatal("missing project must fail")
	}
}

func TestListSkipsNamesDirRejects(t *testing.T) {
	root := t.TempDir()
	for _, d := range []string{"My App", "проект", "ok-app"} {
		if err := os.Mkdir(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	r := Registry{Root: root}
	names, err := r.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(names) != 1 || names[0] != "ok-app" {
		t.Fatalf("names: %v (every listed project must open with Dir)", names)
	}
}

func TestBadNames(t *testing.T) {
	r := Registry{Root: t.TempDir()}
	for _, name := range []string{"", "../etc", "/etc", ".hidden", "a/b", "..", "x y"} {
		if _, err := r.Dir(name); !errors.Is(err, ErrBadName) {
			t.Errorf("Dir(%q): want ErrBadName, got %v", name, err)
		}
		if _, err := r.Create(context.Background(), name); !errors.Is(err, ErrBadName) {
			t.Errorf("Create(%q): want ErrBadName, got %v", name, err)
		}
		if err := r.Delete(name); !errors.Is(err, ErrBadName) {
			t.Errorf("Delete(%q): want ErrBadName, got %v", name, err)
		}
	}
}

func TestDelete(t *testing.T) {
	root := t.TempDir()
	r := Registry{Root: root}
	dir, err := r.Create(context.Background(), "junk")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := r.Delete("junk"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("project still there: %v", err)
	}
	if err := r.Delete("junk"); err == nil {
		t.Fatal("deleting a missing project must fail")
	}
}

// A project that is a symlink loses only the link, never the target.
func TestDeleteSymlinkKeepsTarget(t *testing.T) {
	root, target := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(target, "keep.txt"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	if err := (Registry{Root: root}).Delete("link"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(root, "link")); !os.IsNotExist(err) {
		t.Fatalf("link still there: %v", err)
	}
	if _, err := os.Stat(filepath.Join(target, "keep.txt")); err != nil {
		t.Fatalf("target touched: %v", err)
	}
}

func TestCreate(t *testing.T) {
	root := t.TempDir()
	r := Registry{Root: root}
	dir, err := r.Create(context.Background(), "new-app")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
		t.Fatalf("git repo not initialised: %v", err)
	}
	if _, err := r.Create(context.Background(), "new-app"); err == nil {
		t.Fatal("creating an existing project must fail")
	}
}
