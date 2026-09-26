package profiles

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadMissingGivesFull(t *testing.T) {
	f, err := Load(filepath.Join(t.TempDir(), "none.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	name, p, err := f.Resolve("", "demo", "full")
	if err != nil || name != "full" || strings.Join(p.SettingSources, ",") != "user,project,local" {
		t.Fatalf("%q %+v %v", name, p, err)
	}
}

func TestResolve(t *testing.T) {
	path := filepath.Join(t.TempDir(), "profiles.yaml")
	_ = os.WriteFile(path, []byte(`
profiles:
  lean:
    setting_sources: [user, project, local]
    env:
      ECC_GATEGUARD: "off"
    settings:
      enabledPlugins:
        "caveman@caveman": false
projects:
  demo:
    profile: lean
`), 0o600)
	f, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if names := strings.Join(f.Names(), ","); names != "full,lean" {
		t.Fatalf("names: %s", names)
	}
	name, p, _ := f.Resolve("", "demo", "full")
	if name != "lean" || p.Env["ECC_GATEGUARD"] != "off" || !strings.Contains(p.SettingsJSON(), `"caveman@caveman":false`) {
		t.Fatalf("project default: %q %+v %s", name, p, p.SettingsJSON())
	}
	if name, _, _ := f.Resolve("full", "demo", "full"); name != "full" {
		t.Fatalf("explicit wins: %q", name)
	}
	if name, _, _ := f.Resolve("", "other", "full"); name != "full" {
		t.Fatalf("node default: %q", name)
	}
	if _, _, err := f.Resolve("nope", "demo", "full"); err == nil {
		t.Fatal("unknown profile must fail")
	}
	if full, _ := f.Profiles["full"]; full.SettingsJSON() != "" {
		t.Fatal("no settings override means no --settings")
	}
}

func TestLoadBadYAML(t *testing.T) {
	path := filepath.Join(t.TempDir(), "p.yaml")
	_ = os.WriteFile(path, []byte("profiles: [oops"), 0o600)
	if _, err := Load(path); err == nil {
		t.Fatal("bad yaml must fail")
	}
}
