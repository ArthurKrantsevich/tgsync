package config

import (
	"strings"
	"testing"
	"time"
)

func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func valid() map[string]string {
	return map[string]string{
		"TELEGRAM_BOT_TOKEN": "123:abc",
		"ALLOWED_USER_IDS":   "111, 222",
		"GROUP_CHAT_ID":      "-1001234567890",
		"PROJECTS_ROOT":      osPath("/home/u/Projects"),
		"NODE_NAME":          "laptop-1",
	}
}

func TestLoadValid(t *testing.T) {
	c, err := Load(env(valid()))
	if err != nil {
		t.Fatal(err)
	}
	if c.GroupChatID != -1001234567890 || c.NodeName != "laptop-1" || c.ProjectsRoot != osPath("/home/u/Projects") {
		t.Fatalf("bad config: %+v", c)
	}
	if len(c.AllowedUserIDs) != 2 || !c.IsAllowed(222) || c.IsAllowed(333) {
		t.Fatalf("ids: %v", c.AllowedUserIDs)
	}
	if c.IdleTimeout != 2*time.Hour || c.StallWarn != 20*time.Minute || c.RemindEvery != 2*time.Hour || c.MaxTurn != 0 {
		t.Fatalf("durations: %+v", c)
	}
	dm := valid()
	dm["IDLE_TIMEOUT"], dm["MAX_TURN_DURATION"] = "0", "90m"
	if c4, err := Load(env(dm)); err != nil || c4.IdleTimeout != 0 || c4.MaxTurn != 90*time.Minute {
		t.Fatalf("custom durations: %+v %v", c4, err)
	}
	if c.SudoMode != "off" {
		t.Fatalf("SUDO_MODE must default to off: %q", c.SudoMode)
	}
	s := valid()
	s["SUDO_MODE"], s["SUDO_PASSWORD"] = "env", "pw"
	if c3, err := Load(env(s)); err != nil || c3.SudoMode != "env" || c3.SudoPassword != "pw" {
		t.Fatalf("sudo env: %+v %v", c3, err)
	}
	if len(c.AutoSendGlobs) != 0 {
		t.Fatalf("AUTO_SEND_GLOBS must be empty by default: %v", c.AutoSendGlobs)
	}
	m := valid()
	m["AUTO_SEND_GLOBS"] = " **/*.md, *.pdf ,"
	if c2, _ := Load(env(m)); len(c2.AutoSendGlobs) != 2 || c2.AutoSendGlobs[0] != "**/*.md" {
		t.Fatalf("globs: %v", c2.AutoSendGlobs)
	}
	if c.DBPath != "./data/tgsync.db" || c.MaxParallel != 3 {
		t.Fatalf("defaults: %+v", c)
	}
}

func TestLoadHostnameDefault(t *testing.T) {
	m := valid()
	delete(m, "NODE_NAME")
	c, err := Load(env(m))
	if err != nil {
		t.Fatal(err)
	}
	if c.NodeName == "" {
		t.Fatal("NODE_NAME must default to hostname")
	}
}

func TestLoadErrors(t *testing.T) {
	cases := map[string]struct{ key, value, want string }{
		"no token":       {"TELEGRAM_BOT_TOKEN", "", "TELEGRAM_BOT_TOKEN is required"},
		"bad id":         {"ALLOWED_USER_IDS", "111,abc", `invalid user id "abc"`},
		"no ids":         {"ALLOWED_USER_IDS", " , ", "ALLOWED_USER_IDS is required"},
		"positive group": {"GROUP_CHAT_ID", "12345", "negative supergroup id"},
		"no group":       {"GROUP_CHAT_ID", "", "GROUP_CHAT_ID is required"},
		"relative root":  {"PROJECTS_ROOT", "Projects", "absolute path"},
		"zero parallel":  {"MAX_PARALLEL_SESSIONS", "0", "positive integer"},
		"bad sudo mode":  {"SUDO_MODE", "yes", "SUDO_MODE"},
		"bad duration":   {"STALL_WARN", "soon", "STALL_WARN"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			m := valid()
			m[tc.key] = tc.value
			_, err := Load(env(m))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want error containing %q, got %v", tc.want, err)
			}
		})
	}
}

func TestSudoEnvNeedsPassword(t *testing.T) {
	m := valid()
	m["SUDO_MODE"] = "env"
	if _, err := Load(env(m)); err == nil || !strings.Contains(err.Error(), "SUDO_PASSWORD") {
		t.Fatalf("err: %v", err)
	}
}

func TestLoadSTTDefaults(t *testing.T) {
	c, err := Load(env(valid()))
	if err != nil {
		t.Fatal(err)
	}
	if c.STTURL != "" || c.STTModel != "Systran/faster-whisper-small" || c.STTTimeout != 60*time.Second || c.STTMaxSeconds != 300 {
		t.Fatalf("stt defaults: %q %q %v %d", c.STTURL, c.STTModel, c.STTTimeout, c.STTMaxSeconds)
	}
}

func TestLoadSTTValues(t *testing.T) {
	m := valid()
	m["STT_URL"] = "http://gpu-box:8000"
	m["STT_MODEL"] = "deepdml/faster-whisper-large-v3-turbo-ct2"
	m["STT_TIMEOUT"] = "2m"
	m["STT_MAX_SECONDS"] = "600"
	c, err := Load(env(m))
	if err != nil {
		t.Fatal(err)
	}
	if c.STTURL != "http://gpu-box:8000" || c.STTModel != m["STT_MODEL"] || c.STTTimeout != 2*time.Minute || c.STTMaxSeconds != 600 {
		t.Fatalf("stt: %+v", c)
	}
}

func TestLoadSTTInvalid(t *testing.T) {
	for key, val := range map[string]string{
		"STT_URL":         "gpu-box:8000",
		"STT_MAX_SECONDS": "0",
		"STT_TIMEOUT":     "soon",
	} {
		m := valid()
		m[key] = val
		if _, err := Load(env(m)); err == nil || !strings.Contains(err.Error(), key) {
			t.Errorf("%s=%q: err %v", key, val, err)
		}
	}
}

func TestSudoForcedOffOnWindows(t *testing.T) {
	old := goos
	defer func() { goos = old }()
	goos = "windows"
	m := valid()
	m["SUDO_MODE"] = "telegram"
	c, err := Load(env(m))
	if err != nil {
		t.Fatal(err)
	}
	if c.SudoMode != "off" || !c.SudoUnsupported {
		t.Fatalf("mode=%q unsupported=%v", c.SudoMode, c.SudoUnsupported)
	}
}
