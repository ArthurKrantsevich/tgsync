package permissions

import (
	"context"
	"strings"
	"testing"

	"github.com/ArthurKrantsevich/tgsync/internal/i18n"
	"github.com/ArthurKrantsevich/tgsync/internal/testutil"
)

func TestPromptInEnglish(t *testing.T) {
	i18n.Set(i18n.EN)
	t.Cleanup(func() { i18n.Set(i18n.RU) })
	f := newFixture(t)
	ch := f.ask(context.Background(), "Bash", map[string]any{"command": "go test ./..."})
	testutil.Eventually(t, "prompt", func() bool { return len(f.api.Messages(thread)) == 1 })
	for _, label := range []string{"✅ Allow", "♾ Always: go test"} {
		if _, ok := f.api.Button(thread, label); !ok {
			t.Errorf("no %q button", label)
		}
	}
	f.press(t, "❌ Deny")
	if d := decision(t, ch); d.Allow || !strings.Contains(d.Message, "The user denied this action") {
		t.Fatalf("decision: %+v", d)
	}
	m := f.api.Messages(thread)[0]
	if !strings.Contains(m.HTML, "Permission request") || !strings.Contains(m.HTML, "❌ Denied.") {
		t.Fatalf("message: %q", m.HTML)
	}
}
