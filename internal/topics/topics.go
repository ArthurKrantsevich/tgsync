// Package topics manages the node's forum topics.
package topics

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"unicode/utf16"

	"github.com/ArthurKrantsevich/tgsync/internal/store"
	"github.com/ArthurKrantsevich/tgsync/internal/telegram"
)

const maxName = 128

// colors are the topic icon colours Telegram allows.
var colors = []int{0x6FB9F0, 0xFFD67E, 0xCB86DB, 0x8EEE98, 0xFF93B2, 0xFB6F5F}

// Color is the node's topic colour, stable per node name.
func Color(node string) int {
	h := fnv.New32a()
	h.Write([]byte(node))
	return colors[h.Sum32()%uint32(len(colors))]
}

// Icon is a topic icon role.
type Icon int

const (
	IconActive Icon = iota
	IconClosed
	IconFailed
)

// iconPrefs lists emoji per role, best first. Bots may only use emoji from
// getForumTopicIconStickers, so each role has fallbacks.
var iconPrefs = map[Icon][]string{
	IconActive: {"💻", "🤖", "💬"},
	IconClosed: {"✅", "✔", "👍", "🏁"},
	IconFailed: {"❌", "❗", "‼", "⚠"},
}

// Manager creates and renames topics.
type Manager struct {
	api  telegram.API
	st   *store.Store
	node string

	ensureMu sync.Mutex // one EnsureControl at a time
	mu       sync.Mutex
	control  int
	icons    map[Icon]string
}

func New(api telegram.API, st *store.Store, node string) *Manager {
	return &Manager{api: api, st: st, node: node}
}

// Node returns the node name.
func (m *Manager) Node() string { return m.node }

// Control returns the current control topic id.
func (m *Manager) Control() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.control
}

// EnsureControl returns the control topic, creating it on first start and
// again when the user deleted it. Calls run one at a time: the card refresh
// and /control may both notice a deleted topic, and only one new topic may
// come of it.
func (m *Manager) EnsureControl(ctx context.Context) (int, error) {
	m.ensureMu.Lock()
	defer m.ensureMu.Unlock()
	name := "🖥 " + m.node
	id, err := m.st.ControlTopic(ctx)
	if err != nil {
		return 0, err
	}
	if id != 0 {
		err := m.api.EditTopic(ctx, id, name)
		if err == nil {
			m.setControl(id)
			return id, nil
		}
		if !errors.Is(err, telegram.ErrTopicGone) {
			return 0, err
		}
	}
	id, err = m.api.CreateTopic(ctx, name, Color(m.node), "")
	if err != nil {
		return 0, err
	}
	if err := m.st.SetControlTopic(ctx, id); err != nil {
		return 0, err
	}
	m.setControl(id)
	return id, nil
}

func (m *Manager) setControl(id int) {
	m.mu.Lock()
	m.control = id
	m.mu.Unlock()
}

// Alive reports whether a session topic still exists. It re-applies the
// fixed name, which Telegram treats as a no-op for a live topic.
func (m *Manager) Alive(ctx context.Context, threadID int, project, title string) (bool, error) {
	err := m.api.EditTopic(ctx, threadID, Name(project, title))
	if errors.Is(err, telegram.ErrTopicGone) {
		return false, nil
	}
	return err == nil, err
}

// CreateSession creates a topic for a new session.
func (m *Manager) CreateSession(ctx context.Context, project, title string) (int, error) {
	return m.api.CreateTopic(ctx, Name(project, title), Color(m.node), m.icon(IconActive))
}

// Rename sets a new session title. Call it only when the title changes.
func (m *Manager) Rename(ctx context.Context, threadID int, project, title string) error {
	return m.api.EditTopic(ctx, threadID, Name(project, title))
}

// LoadIcons resolves the icon roles against the emoji Telegram allows.
func (m *Manager) LoadIcons(ctx context.Context) error {
	set, err := m.api.TopicIcons(ctx)
	if err != nil {
		return err
	}
	norm := make(map[string]string, len(set))
	for e, id := range set {
		norm[strings.ReplaceAll(e, "️", "")] = id
	}
	icons := map[Icon]string{}
	for role, prefs := range iconPrefs {
		for _, e := range prefs {
			if id, ok := norm[e]; ok {
				icons[role] = id
				break
			}
		}
	}
	m.mu.Lock()
	m.icons = icons
	m.mu.Unlock()
	return nil
}

func (m *Manager) icon(i Icon) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.icons[i]
}

// SetIcon sets a role icon. A role Telegram has no emoji for is skipped.
func (m *Manager) SetIcon(ctx context.Context, threadID int, i Icon) error {
	id := m.icon(i)
	if id == "" {
		return nil
	}
	return m.api.SetTopicIcon(ctx, threadID, id)
}

// Finish ends a session topic. An empty session (no agent turn) loses its
// topic; any other gets the closed icon and is closed. An empty topic the
// bot cannot delete (no can_delete_messages) is closed instead. The name
// never changes: every rename posts a service message into the topic.
func (m *Manager) Finish(ctx context.Context, threadID int, empty bool) error {
	if empty {
		err := m.Remove(ctx, threadID)
		if err == nil {
			return nil
		}
		slog.Warn("remove empty topic, closing instead", "thread", threadID, "err", err)
	}
	if err := m.SetIcon(ctx, threadID, IconClosed); err != nil {
		slog.Warn("closed icon", "thread", threadID, "err", err)
	}
	return m.api.CloseTopic(ctx, threadID)
}

// Remove deletes a session topic and records it. A topic that is already
// gone counts as removed.
func (m *Manager) Remove(ctx context.Context, threadID int) error {
	if err := m.api.RemoveTopic(ctx, threadID); err != nil && !errors.Is(err, telegram.ErrTopicGone) {
		return err
	}
	return m.st.MarkTopicDeleted(ctx, threadID)
}

// Name builds "<project> · <title>", cut to the Telegram limit
// of 128 UTF-16 units.
func Name(project, title string) string {
	n := project + " · " + title
	if len(utf16.Encode([]rune(n))) <= maxName {
		return n
	}
	// Telegram counts the limit in UTF-16 units; keep room for "…".
	var b strings.Builder
	units := 0
	for _, r := range n {
		w := utf16.RuneLen(r)
		if units+w > maxName-1 {
			break
		}
		b.WriteRune(r)
		units += w
	}
	return b.String() + "…"
}

// Link returns a t.me link to a topic of a supergroup.
func Link(chatID int64, threadID int) string {
	id := strings.TrimPrefix(strconv.FormatInt(chatID, 10), "-100")
	return fmt.Sprintf("https://t.me/c/%s/%d", id, threadID)
}
