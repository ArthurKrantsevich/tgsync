package sudo

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestRewriteCommand(t *testing.T) {
	const tok = "T"
	pre := "TGSYNC_SUDO_TOKEN=T "
	cases := map[string]struct {
		want string
		n    int
	}{
		"sudo apt install x":             {pre + "sudo -A apt install x", 1},
		"echo y | sudo tee /etc/x":       {"echo y | " + pre + "sudo -A tee /etc/x", 1},
		"sudo -A ls":                     {pre + "sudo -A ls", 1},
		"sudo a && sudo b":               {pre + "sudo -A a && " + pre + "sudo -A b", 2},
		"/usr/bin/sudo systemctl reboot": {pre + "/usr/bin/sudo -A systemctl reboot", 1},
		"FOO=1 sudo env":                 {"FOO=1 " + pre + "sudo -A env", 1},
		"x=$(sudo cat /etc/shadow)":      {"x=$(" + pre + "sudo -A cat /etc/shadow)", 1},
		`git commit -m "fix sudo docs"`:  {`git commit -m "fix sudo docs"`, 0},
		`echo "sudo sudo sudo"`:          {`echo "sudo sudo sudo"`, 0},
		"echo pseudo":                    {"echo pseudo", 0},
		"sudo -n whoami":                 {pre + "sudo -A whoami", 1},
		"sudo -u root -n -S id":          {pre + "sudo -A -u root id", 1},
		"sudo --non-interactive true":    {pre + "sudo -A true", 1},
		"sudo -A -n ls":                  {pre + "sudo -A ls", 1},
		"sudo ls -n":                     {pre + "sudo -A ls -n", 1},
		"sudo -nA id":                    {pre + "sudo -A id", 1},
		"sudo -Sn id":                    {pre + "sudo -A id", 1},
		"sudo -nu root id":               {pre + "sudo -A -u root id", 1},
		"sudo -unobody id":               {pre + "sudo -A -unobody id", 1},
	}
	for in, c := range cases {
		got, n, err := RewriteCommand(in, tok)
		if err != nil || got != c.want || n != c.n {
			t.Errorf("RewriteCommand(%q) = %q, %d, %v; want %q, %d", in, got, n, err, c.want, c.n)
		}
	}
	if _, _, err := RewriteCommand("echo 'unterminated", tok); err == nil {
		t.Error("unparsable command must fail")
	}
	if !Uses("/usr/bin/sudo x") || !Uses("a; sudo b") || Uses("pseudo") || Uses(`echo "sudo"`) {
		t.Error("Uses detection is wrong")
	}
	for _, c := range []string{"env sudo id", "xargs sudo rm", "find / -exec sudo rm {} ;", "timeout 5 sudo id",
		`sh -c "sudo id"`, `bash -c 'x; sudo id'`, "/bin/bash -c 'sudo id'"} {
		if !Uses(c) {
			t.Errorf("Uses(%q) = false, want true", c)
		}
		if _, n, _ := RewriteCommand(c, tok); n != 0 {
			t.Errorf("RewriteCommand(%q) must not rewrite a wrapped call", c)
		}
	}
	for _, c := range []string{"cat <<'EOF'\nsudo x\nEOF", `git commit -m "fix sudo"`} {
		if Uses(c) {
			t.Errorf("Uses(%q) = true, want false", c)
		}
	}
}

func serve(t *testing.T, s *Server) string {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() { _ = s.Serve(ctx) }()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if err := Ping(s.Socket()); err == nil {
			return s.Socket()
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("server did not start")
	return ""
}

func askpass(sock string, token string) (string, error) {
	var out bytes.Buffer
	err := Askpass(sock, token, &out)
	return strings.TrimSpace(out.String()), err
}

func newServer(t *testing.T, mode, pw string, ask AskFunc) *Server {
	s := NewServer(filepath.Join(t.TempDir(), "a.sock"), mode, pw, ask)
	s.peerCheck = nil // tests connect directly, not through sudo
	return s
}

func TestEnvModeNeedsToken(t *testing.T) {
	s := newServer(t, ModeEnv, "s3cret", nil)
	sock := serve(t, s)
	if _, err := askpass(sock, "guess"); err == nil {
		t.Fatal("unknown token must mean no password")
	}
	tok := NewToken()
	s.Grant(7, tok, 2)
	for i := 0; i < 2; i++ {
		if pw, err := askpass(sock, tok); err != nil || pw != "s3cret" {
			t.Fatalf("use %d: %q %v", i, pw, err)
		}
	}
	if _, err := askpass(sock, tok); err == nil {
		t.Fatal("token must be used up")
	}
	other := NewToken()
	s.Grant(8, other, 1)
	s.Revoke(8)
	if _, err := askpass(sock, other); err == nil {
		t.Fatal("revoked grant must not apply")
	}
	if a, b := NewToken(), NewToken(); a == b {
		t.Fatal("tokens must be random")
	}
}

func TestGrantExpires(t *testing.T) {
	now := time.Now()
	s := newServer(t, ModeEnv, "pw", nil)
	s.now = func() time.Time { return now }
	sock := serve(t, s)
	tok := NewToken()
	s.Grant(1, tok, 1)
	now = now.Add(GrantTTL + time.Second)
	if _, err := askpass(sock, tok); err == nil {
		t.Fatal("expired grant must not apply")
	}
}

func TestPeerMustBeSudo(t *testing.T) {
	s := NewServer(filepath.Join(t.TempDir(), "a.sock"), ModeEnv, "pw", nil)
	sock := serve(t, s)
	tok := NewToken()
	s.Grant(1, tok, 1)
	if _, err := askpass(sock, tok); err == nil {
		t.Fatal("a caller that is not started by sudo must be refused")
	}
}

func TestTelegramModeAsksUser(t *testing.T) {
	var asked int
	s := newServer(t, ModeTelegram, "", func(ctx context.Context, thread int) (string, error) {
		asked = thread
		if thread == 2 {
			return "", errors.New("отклонено")
		}
		return "typed", nil
	})
	sock := serve(t, s)
	a, b := NewToken(), NewToken()
	s.Grant(1, a, 1)
	if pw, err := askpass(sock, a); err != nil || pw != "typed" || asked != 1 {
		t.Fatalf("pw=%q err=%v asked=%d", pw, err, asked)
	}
	s.Grant(2, b, 1)
	if _, err := askpass(sock, b); err == nil {
		t.Fatal("refused prompt must fail")
	}
}

// callers makes the server see each askpass call as coming from the sudo
// process whose pid is next in the channel.
func callers(s *Server, pids ...int) {
	ch := make(chan int, len(pids))
	for _, p := range pids {
		ch <- p
	}
	s.peerCheck = func(net.Conn) (int, error) { return <-ch, nil }
}

func TestMistypedPasswordCanBeRetried(t *testing.T) {
	var mu sync.Mutex
	asked := 0
	s := newServer(t, ModeTelegram, "", func(ctx context.Context, thread int) (string, error) {
		mu.Lock()
		defer mu.Unlock()
		asked++
		return fmt.Sprintf("try%d", asked), nil
	})
	// sudo 7 asks three times (two typos), then a fourth time, then sudo 8.
	callers(s, 7, 7, 7, 7, 8)
	sock := serve(t, s)
	tok := NewToken()
	s.Grant(1, tok, 1)
	for i := 1; i <= 3; i++ {
		if pw, err := askpass(sock, tok); err != nil || pw != fmt.Sprintf("try%d", i) {
			t.Fatalf("attempt %d: %q %v", i, pw, err)
		}
	}
	if _, err := askpass(sock, tok); err == nil {
		t.Fatal("a fourth attempt must be refused")
	}
	if _, err := askpass(sock, tok); err == nil {
		t.Fatal("another sudo call beyond the approved ones must be refused")
	}
}

func TestSudoCallsOfOneCommandShareThePassword(t *testing.T) {
	release := make(chan struct{})
	var asked atomic.Int32
	s := newServer(t, ModeTelegram, "", func(ctx context.Context, thread int) (string, error) {
		n := asked.Add(1)
		<-release
		return fmt.Sprintf("typed%d", n), nil
	})
	// sudo a | sudo b: two sudo processes ask at once; later sudo a retries.
	callers(s, 7, 8, 7)
	sock := serve(t, s)
	tok := NewToken()
	s.Grant(1, tok, 2)
	out := make(chan string, 2)
	for i := 0; i < 2; i++ {
		go func() {
			pw, err := askpass(sock, tok)
			if err != nil {
				pw = "error: " + err.Error()
			}
			out <- pw
		}()
	}
	time.Sleep(100 * time.Millisecond)
	close(release)
	for i := 0; i < 2; i++ {
		if pw := <-out; pw != "typed1" {
			t.Fatalf("call %d: %q", i, pw)
		}
	}
	if n := asked.Load(); n != 1 {
		t.Fatalf("the user was asked %d times for one command", n)
	}
	// A second attempt of the same sudo means the answer was wrong: ask again.
	if pw, err := askpass(sock, tok); err != nil || pw != "typed2" {
		t.Fatalf("retry: %q %v", pw, err)
	}
}

func TestEnv(t *testing.T) {
	s := NewServer("/run/a.sock", ModeEnv, "pw", nil)
	env := s.Env(5, "/bin/tgsync")
	if env["SUDO_ASKPASS"] != "/bin/tgsync" || env["TGSYNC_ASKPASS"] != "1" || env["TGSYNC_ASKPASS_SOCK"] != "/run/a.sock" || env["TGSYNC_SUDO_TOKEN"] != "" {
		t.Fatalf("env: %v", env)
	}
	for _, v := range env {
		if strings.Contains(v, "pw") {
			t.Fatal("password must never be in the agent environment")
		}
	}
	if NewServer("/x", ModeOff, "", nil).Env(1, "/bin/tgsync") != nil {
		t.Fatal("off mode sets no env")
	}
}
