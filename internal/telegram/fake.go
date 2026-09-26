package telegram

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
)

// FakeMessage is a message as it looks now (edits applied).
type FakeMessage struct {
	ID       int
	ThreadID int
	HTML     string
	Keyboard Keyboard
	Silent   bool
}

// FakeDocument is an uploaded file.
type FakeDocument struct {
	ID       int
	ThreadID int
	Name     string
	Data     []byte
	Caption  string
}

// FakeTopic is a forum topic.
type FakeTopic struct {
	ID     int
	Name   string
	Closed bool
	Color  int
	Icon   string
}

// Fake is an in-memory API for tests.
type Fake struct {
	Perms            Rights
	Icons            map[string]string
	GroupPhoto       []byte
	GroupDescription string
	GeneralHidden    bool

	mu       sync.Mutex
	nextID   int
	messages []*FakeMessage
	topics   map[int]*FakeTopic
	deleted  map[int]bool
	docs     []FakeDocument
	removed  []int
	files    map[string][]byte
	commands []Command
	answers  []string
	reacts   map[int]string
	pinned   []int
}

func NewFake() *Fake {
	return &Fake{Perms: Rights{ManageTopics: true, PinMessages: true, ChangeInfo: true, DeleteMessages: true},
		Icons:  map[string]string{"✅": "i-ok", "❌": "i-fail", "💻": "i-active"},
		nextID: 100, topics: map[int]*FakeTopic{}, deleted: map[int]bool{}}
}

func (f *Fake) SendMessage(_ context.Context, threadID int, html string, kb Keyboard, silent bool) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.deleted[threadID] {
		return 0, ErrTopicGone
	}
	f.nextID++
	f.messages = append(f.messages, &FakeMessage{ID: f.nextID, ThreadID: threadID, HTML: html, Keyboard: kb, Silent: silent})
	return f.nextID, nil
}

func (f *Fake) EditKeyboard(_ context.Context, msgID int, kb Keyboard) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, m := range f.messages {
		if m.ID == msgID {
			if f.deleted[m.ThreadID] {
				return ErrTopicGone
			}
			m.Keyboard = kb
			return nil
		}
	}
	return fmt.Errorf("%w: message %d not found", ErrMessageGone, msgID)
}

func (f *Fake) EditMessage(_ context.Context, msgID int, html string, kb Keyboard) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, m := range f.messages {
		if m.ID == msgID {
			if f.deleted[m.ThreadID] {
				return ErrTopicGone
			}
			m.HTML, m.Keyboard = html, kb
			return nil
		}
	}
	return fmt.Errorf("%w: message %d not found", ErrMessageGone, msgID)
}

func (f *Fake) CreateTopic(_ context.Context, name string, color int, iconID string) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nextID++
	f.topics[f.nextID] = &FakeTopic{ID: f.nextID, Name: name, Color: color, Icon: iconID}
	return f.nextID, nil
}

func (f *Fake) SetTopicIcon(_ context.Context, threadID int, iconID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	t, err := f.topic(threadID)
	if err == nil {
		t.Icon = iconID
	}
	return err
}

func (f *Fake) RemoveTopic(_ context.Context, threadID int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, err := f.topic(threadID); err != nil {
		return err
	}
	if !f.Perms.DeleteMessages { // deleteForumTopic needs can_delete_messages
		return errors.New("not enough rights to delete topic")
	}
	delete(f.topics, threadID)
	f.deleted[threadID] = true
	return nil
}

func (f *Fake) TopicIcons(context.Context) (map[string]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make(map[string]string, len(f.Icons))
	for k, v := range f.Icons {
		out[k] = v
	}
	return out, nil
}

func (f *Fake) HideGeneral(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.GeneralHidden = true
	return nil
}

func (f *Fake) PinMessage(_ context.Context, msgID int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pinned = append(f.pinned, msgID)
	return nil
}

// Pinned returns pinned message ids in pin order.
func (f *Fake) Pinned() []int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]int(nil), f.pinned...)
}

func (f *Fake) GroupInfo(context.Context) (bool, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.GroupPhoto != nil, f.GroupDescription != "", nil
}

func (f *Fake) SetGroupPhoto(_ context.Context, png []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.GroupPhoto = append([]byte(nil), png...)
	return nil
}

func (f *Fake) SetGroupDescription(_ context.Context, text string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.GroupDescription = text
	return nil
}

func (f *Fake) Rights(context.Context) (Rights, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.Perms, nil
}

func (f *Fake) topic(id int) (*FakeTopic, error) {
	if f.deleted[id] {
		return nil, ErrTopicGone
	}
	t, ok := f.topics[id]
	if !ok {
		return nil, fmt.Errorf("topic %d not found", id)
	}
	return t, nil
}

func (f *Fake) EditTopic(_ context.Context, threadID int, name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	t, err := f.topic(threadID)
	if err == nil {
		t.Name = name
	}
	return err
}

func (f *Fake) CloseTopic(_ context.Context, threadID int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	t, err := f.topic(threadID)
	if err == nil {
		t.Closed = true
	}
	return err
}

func (f *Fake) AnswerCallback(_ context.Context, _ string, text string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.answers = append(f.answers, text)
	return nil
}

// Messages returns the messages of a topic, oldest first.
func (f *Fake) Messages(threadID int) []FakeMessage {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []FakeMessage
	for _, m := range f.messages {
		if m.ThreadID == threadID {
			out = append(out, *m)
		}
	}
	return out
}

// Topic returns a topic, or a zero value when it does not exist.
func (f *Fake) Topic(id int) FakeTopic {
	f.mu.Lock()
	defer f.mu.Unlock()
	if t, ok := f.topics[id]; ok {
		return *t
	}
	return FakeTopic{}
}

// Topics returns all topics ordered by id.
func (f *Fake) Topics() []FakeTopic {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []FakeTopic
	for _, t := range f.topics {
		out = append(out, *t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// Button finds a button whose text starts with textPrefix, newest message first.
func (f *Fake) Button(threadID int, textPrefix string) (Button, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := len(f.messages) - 1; i >= 0; i-- {
		m := f.messages[i]
		if m.ThreadID != threadID {
			continue
		}
		for _, row := range m.Keyboard {
			for _, b := range row {
				if strings.HasPrefix(b.Text, textPrefix) {
					return b, true
				}
			}
		}
	}
	return Button{}, false
}

// Answers returns the texts of answered callbacks.
func (f *Fake) Answers() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.answers...)
}

// DeleteTopic simulates a user deleting a topic in the Telegram client.
func (f *Fake) DeleteTopic(id int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.topics, id)
	f.deleted[id] = true
}

func (f *Fake) SendDocument(_ context.Context, threadID int, name string, data []byte, caption string, _ bool) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.deleted[threadID] {
		return 0, ErrTopicGone
	}
	f.nextID++
	f.docs = append(f.docs, FakeDocument{ID: f.nextID, ThreadID: threadID, Name: name, Data: append([]byte(nil), data...), Caption: caption})
	return f.nextID, nil
}

// Documents returns the files sent to a topic, oldest first.
func (f *Fake) Documents(threadID int) []FakeDocument {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []FakeDocument
	for _, d := range f.docs {
		if d.ThreadID == threadID {
			out = append(out, d)
		}
	}
	return out
}

func (f *Fake) SetReaction(_ context.Context, msgID int, emoji string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.reacts == nil {
		f.reacts = map[int]string{}
	}
	if emoji == "" {
		delete(f.reacts, msgID)
	} else {
		f.reacts[msgID] = emoji
	}
	return nil
}

// Reaction returns the reaction on a message, "" if none.
func (f *Fake) Reaction(msgID int) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.reacts[msgID]
}

func (f *Fake) DeleteMessage(_ context.Context, msgID int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i, m := range f.messages {
		if m.ID == msgID {
			f.messages = append(f.messages[:i], f.messages[i+1:]...)
			f.removed = append(f.removed, msgID)
			return nil
		}
	}
	return fmt.Errorf("message %d not found", msgID)
}

// DeletedMessages returns ids of deleted messages.
func (f *Fake) DeletedMessages() []int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]int(nil), f.removed...)
}

// AddFile makes data downloadable under fileID.
func (f *Fake) AddFile(fileID string, data []byte) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.files == nil {
		f.files = map[string][]byte{}
	}
	f.files[fileID] = data
}

func (f *Fake) DownloadFile(_ context.Context, fileID string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	data, ok := f.files[fileID]
	if !ok {
		return nil, fmt.Errorf("file %s not found", fileID)
	}
	return append([]byte(nil), data...), nil
}

func (f *Fake) SetCommands(_ context.Context, cmds []Command) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.commands = append([]Command(nil), cmds...)
	return nil
}

// Commands returns the registered command menu.
func (f *Fake) Commands() []Command {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Command(nil), f.commands...)
}
