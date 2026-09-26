package telegram

import (
	"context"
	"errors"
	"testing"
)

func TestFake(t *testing.T) {
	f, ctx := NewFake(), context.Background()
	th, _ := f.CreateTopic(ctx, "topic", 0, "")
	id, _ := f.SendMessage(ctx, th, "hi", Keyboard{{{Text: "✅ Да", Data: "p:1:a"}}}, true)
	if err := f.EditMessage(ctx, id, "edited", nil); err != nil {
		t.Fatal(err)
	}
	msgs := f.Messages(th)
	if len(msgs) != 1 || msgs[0].HTML != "edited" || msgs[0].Keyboard != nil || !msgs[0].Silent {
		t.Fatalf("messages: %+v", msgs)
	}
	_, _ = f.SendMessage(ctx, th, "q", Keyboard{{{Text: "✅ Да", Data: "p:2:a"}}}, false)
	if b, ok := f.Button(th, "✅"); !ok || b.Data != "p:2:a" {
		t.Fatalf("button: %+v %v", b, ok)
	}
	_ = f.EditTopic(ctx, th, "renamed")
	_ = f.CloseTopic(ctx, th)
	if tp := f.Topic(th); tp.Name != "renamed" || !tp.Closed {
		t.Fatalf("topic: %+v", tp)
	}
	if err := f.EditMessage(ctx, 9999, "x", nil); !errors.Is(err, ErrMessageGone) {
		t.Fatalf("editing an unknown message must report ErrMessageGone: %v", err)
	}
	_ = f.AnswerCallback(ctx, "cb", "ok")
	if a := f.Answers(); len(a) != 1 || a[0] != "ok" {
		t.Fatalf("answers: %v", a)
	}
}

func TestFakeDeletedTopic(t *testing.T) {
	f, ctx := NewFake(), context.Background()
	th, _ := f.CreateTopic(ctx, "t", 0, "")
	id, _ := f.SendMessage(ctx, th, "hi", nil, false)
	f.DeleteTopic(th)
	if _, err := f.SendMessage(ctx, th, "x", nil, false); !errors.Is(err, ErrTopicGone) {
		t.Fatalf("send: %v", err)
	}
	if err := f.EditMessage(ctx, id, "x", nil); !errors.Is(err, ErrTopicGone) {
		t.Fatalf("edit: %v", err)
	}
	if err := f.EditTopic(ctx, th, "y"); !errors.Is(err, ErrTopicGone) {
		t.Fatalf("edit topic: %v", err)
	}
	if _, err := f.SendMessage(ctx, 0, "general", nil, false); err != nil {
		t.Fatalf("general topic must work: %v", err)
	}
}

func TestFakeDocuments(t *testing.T) {
	f, ctx := NewFake(), context.Background()
	th, _ := f.CreateTopic(ctx, "t", 0, "")
	if _, err := f.SendDocument(ctx, th, "plan.md", []byte("# Plan"), "📄 plan.md", false); err != nil {
		t.Fatal(err)
	}
	docs := f.Documents(th)
	if len(docs) != 1 || docs[0].Name != "plan.md" || string(docs[0].Data) != "# Plan" || docs[0].Caption != "📄 plan.md" {
		t.Fatalf("docs: %+v", docs)
	}
	f.DeleteTopic(th)
	if _, err := f.SendDocument(ctx, th, "x", nil, "", false); !errors.Is(err, ErrTopicGone) {
		t.Fatalf("deleted topic: %v", err)
	}
}

func TestFakeDeleteMessage(t *testing.T) {
	f, ctx := NewFake(), context.Background()
	id, _ := f.SendMessage(ctx, 5, "secret", nil, false)
	if err := f.DeleteMessage(ctx, id); err != nil {
		t.Fatal(err)
	}
	if len(f.Messages(5)) != 0 || len(f.DeletedMessages()) != 1 {
		t.Fatalf("messages=%v deleted=%v", f.Messages(5), f.DeletedMessages())
	}
}

func TestFakeDownload(t *testing.T) {
	f := NewFake()
	f.AddFile("id1", []byte("data"))
	if b, err := f.DownloadFile(context.Background(), "id1"); err != nil || string(b) != "data" {
		t.Fatalf("%q %v", b, err)
	}
	if _, err := f.DownloadFile(context.Background(), "nope"); err == nil {
		t.Fatal("unknown file must fail")
	}
}

func TestFakeCommands(t *testing.T) {
	f := NewFake()
	if err := f.SetCommands(context.Background(), []Command{{Name: "menu", Description: "Меню"}}); err != nil {
		t.Fatal(err)
	}
	if c := f.Commands(); len(c) != 1 || c[0].Name != "menu" {
		t.Fatalf("commands: %+v", c)
	}
}

func TestFakeForumExtras(t *testing.T) {
	f, ctx := NewFake(), context.Background()
	th, err := f.CreateTopic(ctx, "t", 0x6FB9F0, "i-active")
	if err != nil {
		t.Fatal(err)
	}
	if tp := f.Topic(th); tp.Color != 0x6FB9F0 || tp.Icon != "i-active" {
		t.Fatalf("topic: %+v", tp)
	}
	_ = f.SetTopicIcon(ctx, th, "i-ok")
	if f.Topic(th).Icon != "i-ok" {
		t.Fatal("icon not set")
	}
	if err := f.RemoveTopic(ctx, th); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(f.RemoveTopic(ctx, th), ErrTopicGone) {
		t.Fatal("second remove must report ErrTopicGone")
	}
	id, _ := f.SendMessage(ctx, 0, "card", nil, true)
	_ = f.PinMessage(ctx, id)
	if p := f.Pinned(); len(p) != 1 || p[0] != id {
		t.Fatalf("pinned: %v", p)
	}
	_ = f.HideGeneral(ctx)
	_ = f.SetGroupPhoto(ctx, []byte{1})
	_ = f.SetGroupDescription(ctx, "d")
	hasPhoto, hasDesc, _ := f.GroupInfo(ctx)
	if !f.GeneralHidden || !hasPhoto || !hasDesc {
		t.Fatalf("hidden=%v photo=%v desc=%v", f.GeneralHidden, hasPhoto, hasDesc)
	}
	f.Perms.PinMessages = false
	if r, _ := f.Rights(ctx); r.PinMessages || !r.ManageTopics {
		t.Fatalf("rights: %+v", r)
	}
}

func TestFakeEditKeyboard(t *testing.T) {
	f, ctx := NewFake(), context.Background()
	id, _ := f.SendMessage(ctx, 5, "text", Keyboard{{{Text: "a", Data: "a"}}}, false)
	if err := f.EditKeyboard(ctx, id, Keyboard{{{Text: "b", Data: "b"}}}); err != nil {
		t.Fatal(err)
	}
	m := f.Messages(5)[0]
	if m.HTML != "text" || m.Keyboard[0][0].Text != "b" {
		t.Fatalf("%+v", m)
	}
	if err := f.EditKeyboard(ctx, 999, nil); !errors.Is(err, ErrMessageGone) {
		t.Fatalf("missing message: %v", err)
	}
}
