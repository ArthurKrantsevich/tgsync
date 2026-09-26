package config

import (
	"errors"
	"path/filepath"

	"github.com/ArthurKrantsevich/tgsync/internal/i18n"
)

// FindHome picks the directory the node runs in: TGSYNC_HOME when set,
// otherwise the first of the current directory, ~/.config/tgsync and
// <configDir>/tgsync that has a .env, otherwise the current directory.
// configDir is os.UserConfigDir(): %AppData% on Windows,
// ~/Library/Application Support on macOS.
func FindHome(getenv func(string) string, cwd, userHome, configDir string, exists func(string) bool) (string, error) {
	if dir := getenv("TGSYNC_HOME"); dir != "" {
		if !exists(dir) {
			return "", errors.New(i18n.T("config.home_missing", dir))
		}
		return dir, nil
	}
	candidates := []string{cwd, filepath.Join(userHome, ".config", "tgsync")}
	if configDir != "" {
		candidates = append(candidates, filepath.Join(configDir, "tgsync"))
	}
	for _, dir := range candidates {
		if exists(filepath.Join(dir, ".env")) {
			return dir, nil
		}
	}
	return cwd, nil
}
