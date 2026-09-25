package config

import (
	"path/filepath"
	"testing"
)

func TestFindHome(t *testing.T) {
	p := filepath.FromSlash
	cwd, home, confDir := p("/repo"), p("/home/u"), p("/home/u/Library/Application Support")
	xdg := filepath.Join(home, ".config", "tgsync")
	native := filepath.Join(confDir, "tgsync")
	cases := []struct {
		name    string
		env     string
		files   []string
		want    string
		wantErr bool
	}{
		{"explicit", p("/srv/tg"), []string{p("/srv/tg")}, p("/srv/tg"), false},
		{"explicit missing", p("/srv/none"), nil, "", true},
		{"env in cwd", "", []string{filepath.Join(cwd, ".env"), filepath.Join(xdg, ".env")}, cwd, false},
		{"env in xdg config", "", []string{filepath.Join(xdg, ".env"), filepath.Join(native, ".env")}, xdg, false},
		{"env in native config", "", []string{filepath.Join(native, ".env")}, native, false},
		{"nowhere", "", nil, cwd, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			exists := func(p string) bool {
				for _, f := range c.files {
					if f == p {
						return true
					}
				}
				return false
			}
			getenv := func(k string) string {
				if k == "TGSYNC_HOME" {
					return c.env
				}
				return ""
			}
			got, err := FindHome(getenv, cwd, home, confDir, exists)
			if (err != nil) != c.wantErr || got != c.want {
				t.Fatalf("got %q, %v; want %q, err=%v", got, err, c.want, c.wantErr)
			}
			if err != nil && err.Error() != "TGSYNC_HOME="+c.env+": directory does not exist" {
				t.Fatalf("error text: %v", err)
			}
		})
	}
}
