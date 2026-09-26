// Package telegram is the node's view of the Telegram group.
package telegram

import (
	"context"
	"time"

	"github.com/ArthurKrantsevich/tgsync/internal/i18n"
)

// ErrTopicGone means the forum topic was deleted. Telegram sends no update
// for that, so it only shows up as an error on the next call.
var ErrTopicGone error = localError("tg.topic_gone")

// ErrMessageGone means the message to edit no longer exists (the user
// deleted it) or never did.
var ErrMessageGone error = localError("tg.message_gone")

// localError is a sentinel error whose text follows the interface language.
type localError string

func (e localError) Error() string { return i18n.T(string(e)) }

// RetryError is a temporary failure: Telegram asked to slow down (429) or
// could not be reached. After is Telegram's retry_after, or 0 to back off.
type RetryError struct {
	After time.Duration
	// Unsafe: the request may have reached Telegram (for example a timeout
	// after sending), so repeating a send could duplicate it.
	Unsafe bool
	Err    error
}

func (e *RetryError) Error() string {
	return i18n.T("tg.retry", e.Err.Error())
}
func (e *RetryError) Unwrap() error { return e.Err }

// Button is an inline keyboard button. Data must fit in 64 bytes.
type Button struct {
	Text string
	Data string
}

// Keyboard is rows of buttons.
type Keyboard [][]Button

// Update is an incoming message or button press from the node's group.
// ThreadID is 0 for the General topic.
type Update struct {
	UserID       int64
	ThreadID     int
	MessageID    int
	Text         string // message text, or the caption of a file
	File         *File  // attached document or photo
	Voice        *Voice // voice message or audio file
	CallbackID   string
	CallbackData string
}

// File is an attachment sent by the user; download it with DownloadFile.
type File struct {
	ID   string
	Name string
	Size int64
}

// Voice is a voice message or audio file to transcribe; download it with
// DownloadFile. Duration is in seconds.
type Voice struct {
	ID       string
	Name     string
	Size     int64
	Duration int
}

// Command is an entry of the bot's command menu shown when typing "/".
type Command struct {
	Name        string
	Description string
}

// Rights are the bot's admin rights in the group. The owner has all of them.
type Rights struct {
	ManageTopics   bool
	PinMessages    bool
	ChangeInfo     bool
	DeleteMessages bool
}

// MaxDownload is the Bot API limit for files a bot can download.
const MaxDownload = 20 << 20

// Handler processes updates.
type Handler func(ctx context.Context, u Update)

// API is what the node needs from Telegram. All calls target the node's group.
type API interface {
	SendMessage(ctx context.Context, threadID int, html string, kb Keyboard, silent bool) (int, error)
	// EditMessage replaces text and keyboard; a nil kb removes the buttons.
	EditMessage(ctx context.Context, msgID int, html string, kb Keyboard) error
	// EditKeyboard replaces only the buttons; a nil kb removes them.
	EditKeyboard(ctx context.Context, msgID int, kb Keyboard) error
	// CreateTopic creates a forum topic. color and iconID are Telegram's icon
	// color and custom emoji id; 0 / "" mean the default icon.
	CreateTopic(ctx context.Context, name string, color int, iconID string) (int, error)
	SetTopicIcon(ctx context.Context, threadID int, iconID string) error
	EditTopic(ctx context.Context, threadID int, name string) error
	CloseTopic(ctx context.Context, threadID int) error
	// RemoveTopic deletes a forum topic (deleteForumTopic).
	RemoveTopic(ctx context.Context, threadID int) error
	// TopicIcons lists the default forum topic icon stickers, emoji to custom emoji id.
	TopicIcons(ctx context.Context) (map[string]string, error)
	// HideGeneral hides the General topic.
	HideGeneral(ctx context.Context) error
	// SendDocument uploads data as a file; caption is HTML.
	SendDocument(ctx context.Context, threadID int, name string, data []byte, caption string, silent bool) (int, error)
	DeleteMessage(ctx context.Context, msgID int) error
	// SetReaction puts an emoji reaction on a message; "" removes it.
	SetReaction(ctx context.Context, msgID int, emoji string) error
	// PinMessage pins a message silently.
	PinMessage(ctx context.Context, msgID int) error
	DownloadFile(ctx context.Context, fileID string) ([]byte, error)
	// SetCommands registers the "/" command menu for the group.
	SetCommands(ctx context.Context, cmds []Command) error
	AnswerCallback(ctx context.Context, callbackID, text string) error
	// GroupInfo reports whether the group already has a photo and a description.
	GroupInfo(ctx context.Context) (hasPhoto, hasDescription bool, err error)
	SetGroupPhoto(ctx context.Context, png []byte) error
	SetGroupDescription(ctx context.Context, text string) error
	// Rights reports the bot's admin rights in the group.
	Rights(ctx context.Context) (Rights, error)
}
