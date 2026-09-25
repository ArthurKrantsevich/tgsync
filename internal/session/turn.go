package session

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/ArthurKrantsevich/tgsync/internal/files"
	"github.com/ArthurKrantsevich/tgsync/internal/render"
	"github.com/ArthurKrantsevich/tgsync/internal/telegram"
)

// turnInfo identifies a finished turn for its summary.
type turnInfo struct {
	no   int
	base string // git snapshot from the turn start; "" outside git
	end  string // git snapshot from the turn end
}

// turnRef backs the turn buttons of one summary message.
type turnRef struct {
	thread, turn int
	base, end    string
	files        []string
	msg          int // the summary message
	html         string
	kb           telegram.Keyboard
	committed    bool // ✅ pressed: one commit prompt per turn
}

// snapshot takes a git snapshot of the session's project for the turn
// summary. The summary of a turn without one has no figures and buttons, so
// a failure (a timeout, a broken repository) is told to the user once, until
// a snapshot works again. A missing project folder is left to claude, which
// fails on it too.
func (m *Manager) snapshot(ctx context.Context, s *sess, what string) string {
	tree, err := files.Snapshot(ctx, s.row.Cwd)
	if err != nil {
		slog.Warn(what, "thread", s.row.ThreadID, "err", err)
	}
	m.mu.Lock()
	tell := err != nil && !s.snapWarned
	s.snapWarned = err != nil
	m.mu.Unlock()
	if tell {
		if _, serr := os.Stat(s.row.Cwd); serr == nil {
			msg := []rune(err.Error())
			if len(msg) > 300 {
				msg = append(msg[:299], '…')
			}
			m.say(ctx, s, "⚠️ Не удалось снять git-снимок проекта, сводка хода будет без статистики и кнопок: <code>"+
				render.Escape(string(msg))+"</code>", true)
		}
	}
	return tree
}

// turnStats returns the turn's per-file figures, nil outside git or on a
// git error: the summary then looks as it did before snapshots.
func (m *Manager) turnStats(ctx context.Context, s *sess, turn turnInfo, changed []string) []files.Stat {
	if turn.base == "" || turn.end == "" {
		return nil
	}
	stats, err := files.TurnStats(ctx, s.row.Cwd, turn.base, turn.end, changed)
	if err != nil {
		slog.Warn("turn stats", "thread", s.row.ThreadID, "err", err)
		return nil
	}
	return stats
}

// statText is the " +3 −1" suffix of one file line.
func statText(stats []files.Stat, rel string) string {
	for _, st := range stats {
		if st.Rel != rel {
			continue
		}
		switch {
		case st.Gone:
			return " (удалён)"
		case st.Binary:
			return " (бинарный)"
		case st.New:
			return fmt.Sprintf(" +%d (новый)", st.Added)
		case st.Added > 0 || st.Deleted > 0:
			return fmt.Sprintf(" +%d −%d", st.Added, st.Deleted)
		}
	}
	return ""
}

// turnRow is the last row of the summary; commit is false once pressed.
func turnRow(key string, commit bool) []telegram.Button {
	row := []telegram.Button{{Text: "🔀 Diff", Data: "td:" + key}}
	if commit {
		row = append(row, telegram.Button{Text: "✅ Коммит", Data: "tc:" + key})
	}
	return append(row, telegram.Button{Text: "↩ Откатить", Data: "tr:" + key})
}

// turnButton handles the turn summary buttons and returns the callback toast.
func (m *Manager) turnButton(ctx context.Context, u telegram.Update, kind, key string) string {
	m.mu.Lock()
	ref, known := m.turnRefs[key]
	s := m.sessions[ref.thread]
	m.mu.Unlock()
	if !known || s == nil || ref.thread != u.ThreadID {
		return "Кнопка устарела"
	}
	switch kind {
	case "td":
		rels := m.unprotected(s.row.Cwd, ref.files)
		if len(rels) == 0 {
			return "Нет файлов для diff"
		}
		diff, err := files.TurnDiff(ctx, s.row.Cwd, ref.base, ref.end, rels)
		if err != nil {
			return alertText(err)
		}
		name := fmt.Sprintf("turn-%d.diff", ref.turn)
		if _, err := m.d.API.SendDocument(ctx, ref.thread, name, []byte(diff), "🔀 Изменения хода", true); err != nil {
			m.telegramFailed(s, "send turn diff", err)
			return alertText(err)
		}
		return ""
	case "tn":
		_ = m.d.API.EditMessage(ctx, u.MessageID, "Откат отменён.", nil)
		return ""
	}
	// Commit and rollback act on the latest turn of an idle session only.
	// A confirmed rollback marks the session so no turn starts meanwhile.
	m.mu.Lock()
	latest := ref.turn == s.turnNo
	busy := s.inTurn || s.queued || len(s.inbox) > 0 || s.restoring
	if latest && !busy && kind == "ty" {
		s.beginRestore()
	}
	m.mu.Unlock()
	if !latest {
		return "Кнопка устарела"
	}
	if busy {
		return "Агент работает — дождись конца хода"
	}
	switch kind {
	case "tc":
		m.mu.Lock() // a double tap arrives as two callbacks: only one commits
		ref = m.turnRefs[key]
		if ref.committed {
			m.mu.Unlock()
			return "Коммит уже отправлен"
		}
		kb := append(telegram.Keyboard(nil), ref.kb...)
		kb[len(kb)-1] = turnRow(key, false)
		ref.kb, ref.committed = kb, true
		m.turnRefs[key] = ref
		m.mu.Unlock()
		_ = m.d.API.EditMessage(ctx, ref.msg, ref.html, kb)
		list := strings.Join(ref.files, ", ")
		prompt := "Закоммить изменения прошлого хода: " + list + ". Состояние до хода — git-дерево " + ref.base +
			": `git diff " + ref.base + " -- <файл>` покажет только правки хода. Если в этих файлах есть и более ранние" +
			" незакоммиченные правки, спроси пользователя, коммитить ли их. Сообщение — в стиле репозитория."
		if err := m.Message(ctx, ref.thread, prompt); err != nil {
			return alertText(err)
		}
		return "Задача на коммит отправлена"
	case "tr":
		kb := telegram.Keyboard{{{Text: "Да, откатить", Data: "ty:" + key}, {Text: "Нет", Data: "tn:" + key}}}
		text := fmt.Sprintf("↩ Откатить %d файлов к началу хода? Новые файлы будут удалены.", len(ref.files))
		if _, err := m.d.API.SendMessage(ctx, ref.thread, text, kb, true); err != nil {
			m.telegramFailed(s, "send rollback confirmation", err)
			return alertText(err)
		}
		return ""
	case "ty":
		res, err := files.Restore(ctx, s.row.Cwd, ref.base, ref.end, ref.files)
		text := fmt.Sprintf("↩ Откачено %d", len(res.Restored))
		for _, part := range []struct {
			label string
			list  []string
		}{
			{"не тронуты, изменены после хода", res.Changed},
			{"оставлены новые файлы, в ходе менялся .gitignore", res.Kept},
			{"пропущены, git их игнорирует", res.Ignored},
		} {
			if len(part.list) > 0 {
				text += "\n" + part.label + ": " + render.Escape(strings.Join(part.list, ", "))
			}
		}
		if err != nil {
			text += "\n⚠️ " + render.Escape(err.Error())
		}
		_ = m.d.API.EditMessage(ctx, u.MessageID, text, nil)
		_ = m.d.API.EditMessage(ctx, ref.msg, ref.html, nil)
		m.mu.Lock()
		delete(m.turnRefs, key)
		if len(res.Restored) > 0 {
			s.rollbackNote = "(Пользователь откатил изменения прошлого хода в файлах: " + strings.Join(res.Restored, ", ") + ".)"
		}
		s.endRestore()
		m.mu.Unlock()
		m.schedule(ctx) // a message that came during the rollback
		return ""
	}
	return "Кнопка устарела"
}

// beginRestore marks a rollback in progress. Callers hold m.mu.
func (s *sess) beginRestore() {
	s.restoring = true
	s.restored = make(chan struct{})
}

// endRestore ends the rollback and wakes a continuation turn waiting for
// it. Callers hold m.mu.
func (s *sess) endRestore() {
	s.restoring = false
	if s.restored != nil {
		close(s.restored)
		s.restored = nil
	}
}

// unprotected drops tgsync's own files (bot token, database) from rels.
func (m *Manager) unprotected(dir string, rels []string) []string {
	var out []string
	for _, rel := range rels {
		abs := filepath.Join(dir, rel)
		hidden := false
		for _, pr := range m.d.Protected {
			if pr != "" && (files.SamePath(abs, pr) || files.SameFile(abs, pr)) {
				hidden = true
			}
		}
		if !hidden {
			out = append(out, rel)
		}
	}
	return out
}
