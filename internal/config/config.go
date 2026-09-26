// Package config reads node settings from the environment.
package config

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/ArthurKrantsevich/tgsync/internal/i18n"
)

// goos is runtime.GOOS; tests change it.
var goos = runtime.GOOS

// Config holds node settings.
type Config struct {
	BotToken       string
	AllowedUserIDs []int64
	GroupChatID    int64
	NodeName       string
	ProjectsRoot   string
	DBPath         string
	MaxParallel    int
	ClaudeCLIPath  string
	AutoSendGlobs  []string
	SudoMode       string
	SudoPassword   string
	// SudoUnsupported: sudo was asked for on Windows; SudoMode is forced to off.
	SudoUnsupported bool
	IdleTimeout     time.Duration
	MaxTurn         time.Duration
	StallWarn       time.Duration
	RemindEvery     time.Duration
	DefaultProfile  string
	ShowHookOutput  bool
	STTURL          string
	STTModel        string
	STTTimeout      time.Duration // 0: no context deadline; stt.Client still has its own 2-minute backstop
	STTMaxSeconds   int
	Language        i18n.Lang
}

// Load reads the configuration through getenv and reports every problem at once.
func Load(getenv func(string) string) (*Config, error) {
	get := func(k string) string { return strings.TrimSpace(getenv(k)) }
	c := &Config{
		BotToken:      get("TELEGRAM_BOT_TOKEN"),
		NodeName:      get("NODE_NAME"),
		ProjectsRoot:  get("PROJECTS_ROOT"),
		DBPath:        get("DB_PATH"),
		ClaudeCLIPath: get("CLAUDE_CLI_PATH"),
		MaxParallel:   3,
	}
	var errs []error
	if c.BotToken == "" {
		errs = append(errs, errors.New(i18n.T("config.required", "TELEGRAM_BOT_TOKEN")))
	}
	ids, err := parseIDs(get("ALLOWED_USER_IDS"))
	switch {
	case err != nil:
		errs = append(errs, fmt.Errorf("ALLOWED_USER_IDS: %w", err))
	case len(ids) == 0:
		errs = append(errs, errors.New(i18n.T("config.required", "ALLOWED_USER_IDS")))
	}
	c.AllowedUserIDs = ids
	if raw := get("GROUP_CHAT_ID"); raw == "" {
		errs = append(errs, errors.New(i18n.T("config.required", "GROUP_CHAT_ID")))
	} else if id, err := strconv.ParseInt(raw, 10, 64); err != nil || id >= 0 {
		errs = append(errs, errors.New(i18n.T("config.group_id")))
	} else {
		c.GroupChatID = id
	}
	if c.ProjectsRoot == "" {
		errs = append(errs, errors.New(i18n.T("config.required", "PROJECTS_ROOT")))
	} else if !filepath.IsAbs(c.ProjectsRoot) {
		errs = append(errs, errors.New(i18n.T("config.abs_path", "PROJECTS_ROOT")))
	}
	if raw := get("MAX_PARALLEL_SESSIONS"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 {
			errs = append(errs, errors.New(i18n.T("config.positive_int", "MAX_PARALLEL_SESSIONS")))
		} else {
			c.MaxParallel = n
		}
	}
	for _, g := range strings.Split(get("AUTO_SEND_GLOBS"), ",") {
		if g = strings.TrimSpace(g); g != "" {
			c.AutoSendGlobs = append(c.AutoSendGlobs, g)
		}
	}
	c.SudoMode, c.SudoPassword = get("SUDO_MODE"), getenv("SUDO_PASSWORD")
	switch c.SudoMode {
	case "":
		c.SudoMode = "off"
	case "off", "telegram":
	case "env":
		if c.SudoPassword == "" {
			errs = append(errs, errors.New(i18n.T("config.sudo_password")))
		}
	default:
		errs = append(errs, errors.New(i18n.T("config.sudo_mode")))
	}
	if goos == "windows" && c.SudoMode != "off" {
		c.SudoMode, c.SudoUnsupported = "off", true
	}
	durations := []struct {
		key string
		def time.Duration
		dst *time.Duration
	}{
		{"IDLE_TIMEOUT", 2 * time.Hour, &c.IdleTimeout},
		{"MAX_TURN_DURATION", 0, &c.MaxTurn},
		{"STALL_WARN", 20 * time.Minute, &c.StallWarn},
		{"REMIND_EVERY", 2 * time.Hour, &c.RemindEvery},
		// STT_TIMEOUT bounds download plus transcription via the request's
		// context. 0 disables that context deadline, but stt.Client still
		// caps the underlying HTTP call at its own 2-minute default, so a
		// stalled STT server cannot block a topic forever.
		{"STT_TIMEOUT", 60 * time.Second, &c.STTTimeout},
	}
	for _, d := range durations {
		*d.dst = d.def
		switch raw := get(d.key); raw {
		case "":
		case "0":
			*d.dst = 0
		default:
			v, err := time.ParseDuration(raw)
			if err != nil || v < 0 {
				errs = append(errs, errors.New(i18n.T("config.duration", d.key, raw)))
			} else {
				*d.dst = v
			}
		}
	}
	if c.DefaultProfile = get("DEFAULT_PROFILE"); c.DefaultProfile == "" {
		c.DefaultProfile = "full"
	}
	switch get("SHOW_HOOK_OUTPUT") {
	case "", "true", "1", "yes":
		c.ShowHookOutput = true
	case "false", "0", "no":
	default:
		errs = append(errs, errors.New(i18n.T("config.bool", "SHOW_HOOK_OUTPUT")))
	}
	c.STTURL = get("STT_URL")
	if c.STTURL != "" {
		if u, err := url.Parse(c.STTURL); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			errs = append(errs, errors.New(i18n.T("config.stt_url")))
		}
	}
	if c.STTModel = get("STT_MODEL"); c.STTModel == "" {
		c.STTModel = "Systran/faster-whisper-small"
	}
	c.STTMaxSeconds = 300
	if raw := get("STT_MAX_SECONDS"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 {
			errs = append(errs, errors.New(i18n.T("config.positive_int", "STT_MAX_SECONDS")))
		} else {
			c.STTMaxSeconds = n
		}
	}
	c.Language = i18n.Default
	if raw := get("BOT_LANGUAGE"); raw != "" {
		if l, ok := i18n.Parse(raw); ok {
			c.Language = l
		} else {
			errs = append(errs, errors.New(i18n.T("config.language", raw)))
		}
	}
	if c.DBPath == "" {
		c.DBPath = "./data/tgsync.db"
	}
	if c.NodeName == "" {
		c.NodeName, _ = os.Hostname()
	}
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	return c, nil
}

func parseIDs(raw string) ([]int64, error) {
	var ids []int64
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		id, err := strconv.ParseInt(part, 10, 64)
		if err != nil || id <= 0 {
			return nil, errors.New(i18n.T("config.user_id", part))
		}
		ids = append(ids, id)
	}
	return ids, nil
}

// IsAllowed reports whether a Telegram user may control this node.
func (c *Config) IsAllowed(userID int64) bool {
	for _, id := range c.AllowedUserIDs {
		if id == userID {
			return true
		}
	}
	return false
}
