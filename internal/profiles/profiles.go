// Package profiles reads profiles.yaml: named sets of Claude Code settings
// (setting sources, environment, settings override) for agent sessions.
package profiles

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"sort"

	"gopkg.in/yaml.v3"
)

// Profile configures the claude process of a session.
type Profile struct {
	SettingSources []string          `yaml:"setting_sources"`
	Env            map[string]string `yaml:"env"`
	Settings       map[string]any    `yaml:"settings"`
}

// SettingsJSON is the --settings override, or "" when the profile has none.
func (p Profile) SettingsJSON() string {
	if len(p.Settings) == 0 {
		return ""
	}
	b, err := json.Marshal(p.Settings)
	if err != nil {
		return ""
	}
	return string(b)
}

// File is the parsed profiles.yaml.
type File struct {
	Profiles map[string]Profile `yaml:"profiles"`
	Projects map[string]struct {
		Profile string `yaml:"profile"`
	} `yaml:"projects"`
}

// Full is the built-in profile: everything from ~/.claude, like a terminal session.
var Full = Profile{SettingSources: []string{"user", "project", "local"}}

// Load reads path. A missing file gives just the built-in "full" profile.
func Load(path string) (*File, error) {
	f := &File{}
	raw, err := os.ReadFile(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
	case err != nil:
		return nil, err
	default:
		if err := yaml.Unmarshal(raw, f); err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
	}
	if f.Profiles == nil {
		f.Profiles = map[string]Profile{}
	}
	if _, ok := f.Profiles["full"]; !ok {
		f.Profiles["full"] = Full
	}
	return f, nil
}

// Names lists the profiles, sorted.
func (f *File) Names() []string {
	out := make([]string, 0, len(f.Profiles))
	for n := range f.Profiles {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// Resolve picks the profile: the requested name, else the project default,
// else def.
func (f *File) Resolve(name, project, def string) (string, Profile, error) {
	if name == "" {
		name = f.Projects[project].Profile
	}
	if name == "" {
		name = def
	}
	p, ok := f.Profiles[name]
	if !ok {
		return "", Profile{}, fmt.Errorf("профиль %q не найден в profiles.yaml", name)
	}
	if len(p.SettingSources) == 0 {
		p.SettingSources = Full.SettingSources
	}
	return name, p, nil
}
