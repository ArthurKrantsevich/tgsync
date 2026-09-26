package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/joho/godotenv"

	"github.com/ArthurKrantsevich/tgsync/internal/agent"
	"github.com/ArthurKrantsevich/tgsync/internal/brand"
	"github.com/ArthurKrantsevich/tgsync/internal/check"
	"github.com/ArthurKrantsevich/tgsync/internal/config"
	"github.com/ArthurKrantsevich/tgsync/internal/group"
	"github.com/ArthurKrantsevich/tgsync/internal/i18n"
	"github.com/ArthurKrantsevich/tgsync/internal/limits"
	"github.com/ArthurKrantsevich/tgsync/internal/permissions"
	"github.com/ArthurKrantsevich/tgsync/internal/profiles"
	"github.com/ArthurKrantsevich/tgsync/internal/projects"
	"github.com/ArthurKrantsevich/tgsync/internal/router"
	"github.com/ArthurKrantsevich/tgsync/internal/session"
	"github.com/ArthurKrantsevich/tgsync/internal/store"
	"github.com/ArthurKrantsevich/tgsync/internal/stt"
	"github.com/ArthurKrantsevich/tgsync/internal/sudo"
	"github.com/ArthurKrantsevich/tgsync/internal/telegram"
	"github.com/ArthurKrantsevich/tgsync/internal/topics"
)

var version = "dev"

func main() {
	// A Task Scheduler task has no console: TGSYNC_LOG sends logs to a file.
	if p := os.Getenv("TGSYNC_LOG"); p != "" {
		if f, err := os.OpenFile(p, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600); err == nil {
			os.Stderr = f
			log.SetOutput(f)
		}
	}
	// sudo runs this binary as SUDO_ASKPASS; see package sudo.
	if os.Getenv("TGSYNC_ASKPASS") == "1" {
		// The agent inherits the node's environment, BOT_LANGUAGE included.
		setLanguage()
		if err := sudo.Askpass(os.Getenv("TGSYNC_ASKPASS_SOCK"), os.Getenv(sudo.TokenVar), os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	cmd := "run"
	if len(os.Args) > 1 {
		cmd = os.Args[1]
	}
	var err error
	switch cmd {
	case "version":
		fmt.Println(version)
		return
	case "run":
		if err = enterHome(); err == nil {
			err = run()
		}
	case "check":
		if err = enterHome(); err == nil {
			err = runCheck()
		}
	case "profile":
		if err = enterHome(); err == nil {
			err = runProfile()
		}
	default:
		fmt.Fprintln(os.Stderr, "usage: tgsync [run|check|profile|version]")
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "tgsync:", err)
		os.Exit(1)
	}
}

// enterHome switches to the node directory so .env, data/ and relative paths resolve there.
func enterHome() error {
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	userHome, _ := os.UserHomeDir()
	configDir, _ := os.UserConfigDir()
	exists := func(p string) bool { _, err := os.Stat(p); return err == nil }
	dir, err := config.FindHome(os.Getenv, cwd, userHome, configDir, exists)
	if err != nil {
		return err
	}
	return os.Chdir(dir)
}

// loadEnv reads .env and picks the interface language from BOT_LANGUAGE, so
// even the lines printed before the config is validated are localized.
func loadEnv() error {
	if err := godotenv.Load(); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf(".env: %w", err)
	}
	setLanguage()
	return nil
}

// setLanguage picks the interface language from BOT_LANGUAGE before the
// config is validated; an invalid value falls back to the default here and
// is reported by config.Load.
func setLanguage() {
	lang, ok := i18n.Parse(os.Getenv("BOT_LANGUAGE"))
	if !ok {
		lang = i18n.Default
	}
	i18n.Set(lang)
}

// runCheck diagnoses the installation and exits with an error when anything is wrong.
func runCheck() error {
	wd, _ := os.Getwd()
	envErr := loadEnv()
	fmt.Println(i18n.T("cli.check.header", version, wd))
	if envErr != nil {
		return envErr
	}
	cfg, err := config.Load(os.Getenv)
	if err != nil {
		fmt.Println(i18n.T("cli.check.config_bad", err))
		return errors.New(i18n.T("cli.check.fix_env"))
	}
	i18n.Set(cfg.Language)
	fmt.Println(i18n.T("cli.check.config_ok"))
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var checks []check.Check
	api, err := telegram.NewBot(cfg.BotToken, cfg.GroupChatID, func(context.Context, telegram.Update) {})
	if err != nil {
		checks = append(checks, check.Check{Name: i18n.T("check.name.bot"), Run: func(context.Context) (string, error) {
			return "", errors.New(i18n.T("cli.check.bot_token", err))
		}})
	} else {
		checks = append(checks,
			check.Check{Name: i18n.T("check.name.bot"), Run: api.Me},
			check.Check{Name: i18n.T("check.name.forum"), Run: func(ctx context.Context) (string, error) {
				forum, err := api.IsForum(ctx)
				if err != nil {
					return "", errors.New(i18n.T("cli.check.group_unavailable", cfg.GroupChatID, err))
				}
				if !forum {
					return "", errors.New(i18n.T("cli.check.no_topics"))
				}
				return strconv.FormatInt(cfg.GroupChatID, 10), nil
			}},
			check.Check{Name: i18n.T("check.name.rights"), Run: func(ctx context.Context) (string, error) {
				r, err := api.Rights(ctx)
				if err != nil {
					return "", err
				}
				if !r.ManageTopics {
					return "", errors.New(i18n.T("cli.check.need_admin"))
				}
				var extra []string
				if !r.PinMessages {
					extra = append(extra, i18n.T("cli.check.right_pin"))
				}
				if !r.ChangeInfo {
					extra = append(extra, i18n.T("cli.check.right_info"))
				}
				if !r.DeleteMessages {
					extra = append(extra, i18n.T("cli.check.right_delete"))
				}
				if len(extra) > 0 {
					return i18n.T("cli.check.rights_partial", strings.Join(extra, ", ")), nil
				}
				return i18n.T("cli.check.rights_ok"), nil
			}},
		)
	}
	dbDir := filepath.Dir(cfg.DBPath)
	_ = os.MkdirAll(dbDir, 0o700)
	checks = append(checks,
		check.Check{Name: i18n.T("check.name.profiles"), Run: func(context.Context) (string, error) {
			f, err := profiles.Load("profiles.yaml")
			if err != nil {
				return "", err
			}
			if _, _, err := f.Resolve("", "", cfg.DefaultProfile); err != nil {
				return "", fmt.Errorf("DEFAULT_PROFILE: %v", err)
			}
			return i18n.T("cli.check.profiles", strings.Join(f.Names(), ", "), cfg.DefaultProfile), nil
		}},
		check.Check{Name: "sudo", Run: func(context.Context) (string, error) {
			if cfg.SudoUnsupported {
				return i18n.T("cli.check.sudo_windows"), nil
			}
			switch cfg.SudoMode {
			case "env":
				return i18n.T("cli.check.sudo_env"), nil
			case "telegram":
				if api == nil {
					return "", errors.New(i18n.T("cli.check.sudo_no_bot"))
				}
				if r, err := api.Rights(ctx); err != nil || !r.DeleteMessages {
					return "", errors.New(i18n.T("cli.check.sudo_no_delete"))
				}
				return i18n.T("cli.check.sudo_telegram"), nil
			}
			return i18n.T("cli.check.sudo_off"), nil
		}},
		check.ClaudeCLI(cfg.ClaudeCLIPath),
		check.Dir("PROJECTS_ROOT", cfg.ProjectsRoot, true),
		check.Dir(i18n.T("check.name.db"), dbDir, true),
	)
	if !check.Run(ctx, os.Stdout, checks) {
		return errors.New(i18n.T("cli.check.failed"))
	}
	fmt.Println(i18n.T("cli.check.ok"))
	return nil
}

func run() error {
	if err := loadEnv(); err != nil {
		return err
	}
	cfg, err := config.Load(os.Getenv)
	if err != nil {
		return err
	}
	i18n.Set(cfg.Language)
	if cfg.SudoUnsupported {
		slog.Warn("sudo is not available on Windows, SUDO_MODE forced to off")
	}
	// Secrets stay in this process only: claude and every tool it runs inherit
	// the environment. (.env values were set with Setenv, so after Unsetenv
	// they are not visible in /proc/<pid>/environ either.)
	_ = os.Unsetenv("SUDO_PASSWORD")
	_ = os.Unsetenv("TELEGRAM_BOT_TOKEN")
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	// stop() also runs as soon as ctx is cancelled, not only when run()
	// returns: otherwise a second SIGTERM/Ctrl-C during the (possibly slow)
	// shutdown below is swallowed instead of forcing an immediate exit.
	go func() {
		<-ctx.Done()
		stop()
	}()

	st, err := store.Open(cfg.DBPath)
	if err != nil {
		return err
	}
	defer st.Close()

	var rt *router.Router
	api, err := telegram.NewBot(cfg.BotToken, cfg.GroupChatID, func(ctx context.Context, u telegram.Update) { rt.Handle(ctx, u) })
	if err != nil {
		return fmt.Errorf("telegram: %w", err)
	}
	r, err := api.Rights(ctx)
	if err != nil {
		return fmt.Errorf(i18n.T("cli.rights_check"), cfg.GroupChatID, err)
	}
	if !r.ManageTopics {
		return errors.New(i18n.T("cli.need_admin"))
	}

	rapi := telegram.NewResilient(api) // retries 429 and network failures
	tp := topics.New(rapi, st, cfg.NodeName)
	control, err := tp.EnsureControl(ctx) // also recreates a deleted control topic
	if err != nil {
		return fmt.Errorf("control topic: %w", err)
	}

	profileFile, err := profiles.Load("profiles.yaml")
	if err != nil {
		return err
	}
	broker := permissions.NewBroker(rapi, st)
	home, _ := os.Getwd()
	sockPath := sudo.SocketPath(home)
	sudoSrv := sudo.NewServer(sockPath, cfg.SudoMode, cfg.SudoPassword, broker.AskPassword)
	if sudoSrv.Enabled() {
		if err := sudoSrv.Listen(); err != nil {
			return fmt.Errorf(i18n.T("cli.sudo_socket"), sockPath, err)
		}
		broker.SetSudo(sudoSrv)
		go func() {
			if err := sudoSrv.Serve(ctx); err != nil {
				slog.Error("sudo askpass socket", "err", err)
			}
		}()
	}
	exe, _ := os.Executable()
	grp := &group.Group{API: rapi, Store: st, Topics: tp, Avatar: brand.Avatar,
		Description: brand.GroupDescription(), Started: time.Now()}
	// Everything that may post to the control topic (limit notices, /usage)
	// goes through the tracked API, so the hourly sweep removes it.
	ctrl := grp.ControlAPI(rapi)
	tracker := limits.New(ctrl, st, tp.Control)
	if err := tracker.Load(ctx); err != nil {
		slog.Warn("load rate limits", "err", err)
	}
	mgr := session.NewManager(session.Deps{
		API: ctrl, Store: st, Topics: tp, Broker: broker, Runner: agent.SDKRunner{}, Limits: tracker,
		MaxParallel: cfg.MaxParallel, CLIPath: cfg.ClaudeCLIPath,
		SettingSources: []string{"user", "project", "local"},
		Protected:      protectedPaths(home, cfg.DBPath, sockPath),
		EditInterval:   3 * time.Second,
		AutoSend:       cfg.AutoSendGlobs,
		IdleTimeout:    cfg.IdleTimeout,
		StallWarn:      cfg.StallWarn,
		MaxTurn:        cfg.MaxTurn,
		RemindEvery:    cfg.RemindEvery,
		ShowHookOutput: cfg.ShowHookOutput,
		Profiles: func(name, project string) (string, agent.StartOptions, error) {
			name, p, err := profileFile.Resolve(name, project, cfg.DefaultProfile)
			return name, agent.StartOptions{SettingSources: p.SettingSources, Env: p.Env, Settings: p.SettingsJSON()}, err
		},
		AgentEnv: func(thread int) map[string]string { return sudoSrv.Env(thread, exe) },
	})
	if err := mgr.Restore(ctx); err != nil {
		return fmt.Errorf("restore sessions: %w", err)
	}
	defer mgr.Shutdown()

	grp.Forget = mgr.TopicRemoved
	grp.Setup(ctx) // loads topic icons before any new session is created
	if err := grp.EnsureCard(ctx); err != nil {
		slog.Warn("control card", "err", err)
	}

	rt = &router.Router{
		Allowed: cfg.IsAllowed, API: ctrl, ChatID: cfg.GroupChatID, Topics: tp,
		Projects: projects.Registry{Root: cfg.ProjectsRoot}, Sessions: mgr, Broker: broker,
		ClaudeHome: claudeHome(), ProfileNames: profileFile.Names, Group: grp,
	}
	rt.STTTimeout, rt.STTMaxSeconds = cfg.STTTimeout, cfg.STTMaxSeconds
	if cfg.STTURL != "" { // keep rt.STT a nil interface when disabled
		rt.STT = &stt.Client{URL: cfg.STTURL, Model: cfg.STTModel, Timeout: cfg.STTTimeout}
	}
	if err := rapi.SetCommands(ctx, router.Commands()); err != nil {
		slog.Warn("register command menu", "err", err)
	}
	go mgr.Run(ctx)
	go grp.Run(ctx)
	slog.Info("tgsync running", "node", cfg.NodeName, "control_topic", control)
	api.Run(ctx)
	return nil
}

// protectedPaths lists tgsync's own files the agent must never touch: the
// secrets in .env, the database and its WAL files, profiles.yaml (its env
// may hold keys, and it decides what the agent may do) and the sudo askpass
// socket. dir is the node folder (the working directory), db the DB_PATH
// setting, sock the askpass socket.
func protectedPaths(dir, db, sock string) []string {
	abs := func(p string) string {
		if !filepath.IsAbs(p) {
			p = filepath.Join(dir, p)
		}
		return filepath.Clean(p)
	}
	dbPath := abs(db)
	return []string{abs(".env"), dbPath, dbPath + "-wal", dbPath + "-shm", abs("profiles.yaml"), abs(sock)}
}

// claudeHome is where Claude Code keeps its settings and transcripts.
func claudeHome() string {
	if dir := os.Getenv("CLAUDE_CONFIG_DIR"); dir != "" {
		return dir
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".claude")
}

// runProfile sets the bot's avatar and descriptions from package brand.
func runProfile() error {
	if err := loadEnv(); err != nil {
		return err
	}
	cfg, err := config.Load(os.Getenv)
	if err != nil {
		return err
	}
	i18n.Set(cfg.Language)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	api, err := telegram.NewBot(cfg.BotToken, cfg.GroupChatID, func(context.Context, telegram.Update) {})
	if err != nil {
		return fmt.Errorf("telegram: %w", err)
	}
	if err := api.SetProfile(ctx, brand.ShortDescription(), brand.Description(), brand.Avatar); err != nil {
		return err
	}
	fmt.Println(i18n.T("cli.profile_done"))
	return nil
}
