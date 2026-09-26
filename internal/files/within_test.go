package files

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWithinUnix(t *testing.T) {
	cases := []struct {
		base, p string
		fold    bool
		rel     string
		ok      bool
	}{
		{"/p/app", "/p/app", false, ".", true},
		{"/p/app", "/p/app/a.go", false, "a.go", true},
		{"/p/app", "/p/app/src/a.go", false, "src/a.go", true},
		{"/p/app", "/p/app2/a.go", false, "", false},
		{"/p/app", "/p", false, "", false},
		{"/p/app", "/etc/hosts", false, "", false},
		{"/p/app", "/P/App/a.go", false, "", false},
		{"/p/app", "/P/App/a.go", true, "a.go", true},
		{"/", "/etc/hosts", false, "etc/hosts", true},
	}
	for _, c := range cases {
		rel, ok := within(c.base, c.p, "/", c.fold)
		if rel != c.rel || ok != c.ok {
			t.Errorf("within(%q, %q, fold=%v) = %q, %v; want %q, %v", c.base, c.p, c.fold, rel, ok, c.rel, c.ok)
		}
	}
}

func TestWithinWindows(t *testing.T) {
	cases := []struct {
		base, p string
		rel     string
		ok      bool
	}{
		{`C:\p\app`, `C:\p\app\src\a.go`, `src\a.go`, true},
		{`C:\p\app`, `c:\P\APP\a.go`, `a.go`, true},
		{`C:\p\app`, `C:\p\other\a.go`, "", false},
		{`C:\p\app`, `D:\p\app\a.go`, "", false},
		{`C:\p\app`, `C:\p\app2`, "", false},
		{`C:\`, `C:\x\a.go`, `x\a.go`, true},
		{`C:\Users\u\tgsync\.env`, `c:\users\u\tgsync\.ENV`, ".", true},
	}
	for _, c := range cases {
		rel, ok := within(c.base, c.p, `\`, true)
		if rel != c.rel || ok != c.ok {
			t.Errorf("within(%q, %q) = %q, %v; want %q, %v", c.base, c.p, rel, ok, c.rel, c.ok)
		}
	}
}

func TestWithinCleans(t *testing.T) {
	base := t.TempDir()
	sep := string(filepath.Separator)
	if _, ok := Within(base, base+sep+".."+sep+"x"); ok {
		t.Fatal("a path that climbs out with .. must be outside")
	}
	if !SamePath(base, base+sep) {
		t.Fatal("a trailing separator must not matter")
	}
}

func TestAbsWindows(t *testing.T) {
	dir := `C:\p\app`
	cases := map[string]string{
		`src\a.go`:                        `C:\p\app\src\a.go`,
		`C:\x\a.go`:                       `C:\x\a.go`,
		`D:\x`:                            `D:\x`,
		`\Users\u\tgsync\.env`:            `C:\Users\u\tgsync\.env`,
		`/Users/u/tgsync/.env`:            `C:\Users\u\tgsync\.env`,
		`/c/Users/u/tgsync/.env`:          `C:\Users\u\tgsync\.env`,
		`/d/x`:                            `D:\x`,
		`C:/Users\u//tgsync/.env`:         `C:\Users\u\tgsync\.env`,
		`C:\Users\u\tgsync\.env.`:         `C:\Users\u\tgsync\.env`,
		`C:\Users\u\tgsync\.env  `:        `C:\Users\u\tgsync\.env`,
		`C:\Users\u\tgsync\.env::$DATA`:   `C:\Users\u\tgsync\.env`,
		`C:\Users\u\tgsync. \sub\..\.env`: `C:\Users\u\tgsync\.env`,
		`..\..\..\..\x`:                   `C:\x`,
	}
	for in, want := range cases {
		if got := absWindows(dir, in); got != want {
			t.Errorf("absWindows(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSameFile(t *testing.T) {
	dir := t.TempDir()
	env := filepath.Join(dir, ".env")
	if err := os.WriteFile(env, []byte("T"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "hard")
	if err := os.Link(env, link); err != nil {
		t.Skipf("hard links unavailable: %v", err)
	}
	if !SameFile(link, env) {
		t.Fatal("a hard link to the protected file is the same file")
	}
	if SameFile(filepath.Join(dir, "missing"), env) {
		t.Fatal("a missing file is not the same file")
	}
}
