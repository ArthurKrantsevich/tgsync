package render

import (
	"testing"
	"time"

	"github.com/ArthurKrantsevich/tgsync/internal/i18n"
)

// inLang runs f with lang set and restores Russian afterwards.
func inLang(t *testing.T, lang i18n.Lang, f func()) {
	t.Helper()
	i18n.Set(lang)
	defer i18n.Set(i18n.RU)
	f()
}

func TestEnglishTexts(t *testing.T) {
	inLang(t, i18n.EN, func() {
		ctx := ContextText(ContextView{Total: 50_000, Max: 200_000, Percent: 25, AutoCompact: true, AutoCompactPct: 90,
			Categories: []CategoryView{{Name: "Messages", Tokens: 30_000}, {Name: "System tools", Tokens: 20_000}}})
		want := "🧠 <b>Context</b> · 50K / 200K (25%)\nmessages 30K · tools 20K\nAuto-compact: on, at 90%"
		if ctx != want {
			t.Errorf("context:\n%s\nwant:\n%s", ctx, want)
		}
		now := time.Date(2026, 9, 26, 10, 0, 0, 0, time.Local)
		if got := LimitLine("five_hour", 0.72, now.Add(2*time.Hour), now); got != "5 hours: 72% · resets at 12:00" {
			t.Errorf("limit line %q", got)
		}
		if got := ResetTime(now.Add(48*time.Hour), now); got != "Mon 10:00" {
			t.Errorf("reset time %q", got)
		}
		for _, c := range []struct {
			h    Hidden
			want string
		}{
			{Hidden{Running: 1}, "🤖 <b>Agents</b> · 1 running · 0 done\n… 1 more running"},
			{Hidden{Done: 2}, "🤖 <b>Agents</b> · 0 running · 2 done\n… 2 more done"},
		} {
			if got := AgentsList(nil, c.h); got != c.want {
				t.Errorf("agents %q, want %q", got, c.want)
			}
		}
		if got := SessionUsageText(1, 1500, 0, 0.5, ""); got != "📊 <b>This session</b>: 1 turn · 2K tok. · ≈$0.50" {
			t.Errorf("session usage %q", got)
		}
		if got := SessionUsageText(3, 1500, 0, 0.5, ""); got != "📊 <b>This session</b>: 3 turns · 2K tok. · ≈$0.50" {
			t.Errorf("session usage %q", got)
		}
	})
}
