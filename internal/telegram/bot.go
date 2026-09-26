package telegram

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/ArthurKrantsevich/tgsync/internal/i18n"
	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

// downloadTimeout bounds one file download: a stalled one would block the
// topic's queue.
var downloadTimeout = 2 * time.Minute

// Bot implements API with github.com/go-telegram/bot.
type Bot struct {
	b      *bot.Bot
	chatID int64
	files  *http.Client // downloads of user files
}

// NewBot connects to Telegram. Updates from other chats and from bots are dropped.
func NewBot(token string, chatID int64, h Handler) (*Bot, error) {
	return newBot(token, chatID, h)
}

func newBot(token string, chatID int64, h Handler, opts ...bot.Option) (*Bot, error) {
	d := newDispatcher(h)
	opts = append([]bot.Option{
		bot.WithAllowedUpdates(bot.AllowedUpdates{"message", "callback_query"}),
		bot.WithNotAsyncHandlers(),
		bot.WithDefaultHandler(func(ctx context.Context, _ *bot.Bot, u *models.Update) {
			if upd, ok := normalize(u, chatID); ok {
				d.dispatch(ctx, upd)
			}
		}),
	}, opts...)
	b, err := bot.New(token, opts...)
	if err != nil {
		return nil, err
	}
	return &Bot{b: b, chatID: chatID, files: &http.Client{Timeout: downloadTimeout}}, nil
}

// Run polls updates until ctx is done.
func (t *Bot) Run(ctx context.Context) { t.b.Start(ctx) }

func normalize(u *models.Update, chatID int64) (Update, bool) {
	switch {
	case u.Message != nil:
		m := u.Message
		if m.Chat.ID != chatID || m.From == nil || m.From.IsBot {
			return Update{}, false
		}
		u := Update{UserID: m.From.ID, ThreadID: threadOf(m), MessageID: m.ID, Text: m.Text}
		switch {
		case m.Document != nil:
			u.File = &File{ID: m.Document.FileID, Name: m.Document.FileName, Size: m.Document.FileSize}
			u.Text = m.Caption
		case len(m.Photo) > 0:
			best := m.Photo[len(m.Photo)-1]
			for _, p := range m.Photo {
				if p.Width > best.Width {
					best = p
				}
			}
			u.File = &File{ID: best.FileID, Name: "photo.jpg", Size: int64(best.FileSize)}
			u.Text = m.Caption
		case m.Voice != nil:
			u.Voice = &Voice{ID: m.Voice.FileID, Name: "voice.ogg", Size: m.Voice.FileSize, Duration: m.Voice.Duration}
			u.Text = m.Caption
		case m.Audio != nil:
			name := m.Audio.FileName
			if name == "" {
				name = "audio.mp3"
			}
			u.Voice = &Voice{ID: m.Audio.FileID, Name: name, Size: m.Audio.FileSize, Duration: m.Audio.Duration}
			u.Text = m.Caption
		}
		return u, true
	case u.CallbackQuery != nil:
		cq := u.CallbackQuery
		m := cq.Message.Message
		if m == nil || m.Chat.ID != chatID {
			return Update{}, false
		}
		return Update{UserID: cq.From.ID, ThreadID: threadOf(m), MessageID: m.ID,
			CallbackID: cq.ID, CallbackData: cq.Data}, true
	}
	return Update{}, false
}

func threadOf(m *models.Message) int {
	if m.IsTopicMessage {
		return m.MessageThreadID
	}
	return 0
}

func markup(kb Keyboard) *models.InlineKeyboardMarkup {
	rows := make([][]models.InlineKeyboardButton, 0, len(kb))
	for _, r := range kb {
		row := make([]models.InlineKeyboardButton, 0, len(r))
		for _, b := range r {
			row = append(row, models.InlineKeyboardButton{Text: b.Text, CallbackData: b.Data})
		}
		rows = append(rows, row)
	}
	return &models.InlineKeyboardMarkup{InlineKeyboard: rows}
}

var tagRe = regexp.MustCompile(`<[^>]+>`)

// plainText strips HTML for the fallback when Telegram rejects our markup.
func plainText(s string) string { return html.UnescapeString(tagRe.ReplaceAllString(s, "")) }

func isParseError(err error) bool {
	return err != nil && strings.Contains(err.Error(), "can't parse entities")
}

// mapErr turns Telegram's "topic deleted" answers into ErrTopicGone.
func mapErr(err error) error {
	if err == nil {
		return nil
	}
	var tooMany *bot.TooManyRequestsError
	if errors.As(err, &tooMany) {
		return &RetryError{After: time.Duration(tooMany.RetryAfter) * time.Second, Err: err}
	}
	var opErr *net.OpError
	if errors.As(err, &opErr) && opErr.Op == "dial" {
		return &RetryError{Err: err} // not connected: nothing was sent
	}
	var netErr net.Error
	var urlErr *url.Error
	if errors.As(err, &urlErr) || errors.As(err, &netErr) {
		return &RetryError{Unsafe: true, Err: err}
	}
	e := err.Error()
	if strings.Contains(e, "message thread not found") || strings.Contains(e, "TOPIC_ID_INVALID") || strings.Contains(e, "TOPIC_DELETED") {
		return fmt.Errorf("%w: %v", ErrTopicGone, err)
	}
	if strings.Contains(e, "message to edit not found") || strings.Contains(e, "MESSAGE_ID_INVALID") {
		return fmt.Errorf("%w: %v", ErrMessageGone, err)
	}
	return err
}

func notModified(err error) bool {
	if err == nil {
		return false
	}
	e := strings.ToLower(err.Error())
	return strings.Contains(e, "not modified") || strings.Contains(e, "not_modified")
}

func (t *Bot) SendMessage(ctx context.Context, threadID int, text string, kb Keyboard, silent bool) (int, error) {
	p := &bot.SendMessageParams{
		ChatID: t.chatID, MessageThreadID: threadID, Text: text, ParseMode: models.ParseModeHTML,
		DisableNotification: silent, LinkPreviewOptions: &models.LinkPreviewOptions{IsDisabled: bot.True()},
	}
	if kb != nil {
		p.ReplyMarkup = markup(kb)
	}
	m, err := t.b.SendMessage(ctx, p)
	if isParseError(err) {
		p.ParseMode, p.Text = "", plainText(text)
		m, err = t.b.SendMessage(ctx, p)
	}
	if err != nil {
		return 0, mapErr(err)
	}
	return m.ID, nil
}

func (t *Bot) EditMessage(ctx context.Context, msgID int, text string, kb Keyboard) error {
	p := &bot.EditMessageTextParams{
		ChatID: t.chatID, MessageID: msgID, Text: text, ParseMode: models.ParseModeHTML,
		LinkPreviewOptions: &models.LinkPreviewOptions{IsDisabled: bot.True()},
	}
	if kb != nil {
		p.ReplyMarkup = markup(kb)
	}
	_, err := t.b.EditMessageText(ctx, p)
	if isParseError(err) {
		p.ParseMode, p.Text = "", plainText(text)
		_, err = t.b.EditMessageText(ctx, p)
	}
	if notModified(err) {
		return nil
	}
	return mapErr(err)
}

func (t *Bot) EditKeyboard(ctx context.Context, msgID int, kb Keyboard) error {
	p := &bot.EditMessageReplyMarkupParams{ChatID: t.chatID, MessageID: msgID}
	if kb != nil {
		p.ReplyMarkup = markup(kb)
	}
	_, err := t.b.EditMessageReplyMarkup(ctx, p)
	if notModified(err) {
		return nil
	}
	return mapErr(err)
}

func (t *Bot) CreateTopic(ctx context.Context, name string, color int, iconID string) (int, error) {
	ft, err := t.b.CreateForumTopic(ctx, &bot.CreateForumTopicParams{ChatID: t.chatID, Name: name,
		IconColor: color, IconCustomEmojiID: iconID})
	if err != nil {
		return 0, mapErr(err)
	}
	return ft.MessageThreadID, nil
}

func (t *Bot) SetTopicIcon(ctx context.Context, threadID int, iconID string) error {
	_, err := t.b.EditForumTopic(ctx, &bot.EditForumTopicParams{ChatID: t.chatID, MessageThreadID: threadID, IconCustomEmojiID: iconID})
	if notModified(err) {
		return nil
	}
	return mapErr(err)
}

func (t *Bot) RemoveTopic(ctx context.Context, threadID int) error {
	_, err := t.b.DeleteForumTopic(ctx, &bot.DeleteForumTopicParams{ChatID: t.chatID, MessageThreadID: threadID})
	return mapErr(err)
}

func (t *Bot) TopicIcons(ctx context.Context) (map[string]string, error) {
	st, err := t.b.GetForumTopicIconStickers(ctx)
	if err != nil {
		return nil, mapErr(err)
	}
	out := make(map[string]string, len(st))
	for _, s := range st {
		out[s.Emoji] = s.CustomEmojiID
	}
	return out, nil
}

func (t *Bot) HideGeneral(ctx context.Context) error {
	_, err := t.b.HideGeneralForumTopic(ctx, &bot.HideGeneralForumTopicParams{ChatID: t.chatID})
	if notModified(err) {
		return nil
	}
	return mapErr(err)
}

func (t *Bot) PinMessage(ctx context.Context, msgID int) error {
	_, err := t.b.PinChatMessage(ctx, &bot.PinChatMessageParams{ChatID: t.chatID, MessageID: msgID, DisableNotification: true})
	return mapErr(err)
}

func (t *Bot) GroupInfo(ctx context.Context) (hasPhoto, hasDescription bool, err error) {
	c, err := t.b.GetChat(ctx, &bot.GetChatParams{ChatID: t.chatID})
	if err != nil {
		return false, false, mapErr(err)
	}
	return c.Photo != nil, c.Description != "", nil
}

func (t *Bot) SetGroupPhoto(ctx context.Context, png []byte) error {
	_, err := t.b.SetChatPhoto(ctx, &bot.SetChatPhotoParams{ChatID: t.chatID,
		Photo: &models.InputFileUpload{Filename: "avatar.png", Data: bytes.NewReader(png)}})
	return mapErr(err)
}

func (t *Bot) SetGroupDescription(ctx context.Context, text string) error {
	_, err := t.b.SetChatDescription(ctx, &bot.SetChatDescriptionParams{ChatID: t.chatID, Description: text})
	if notModified(err) {
		return nil
	}
	return mapErr(err)
}

func (t *Bot) Rights(ctx context.Context) (Rights, error) {
	m, err := t.b.GetChatMember(ctx, &bot.GetChatMemberParams{ChatID: t.chatID, UserID: t.b.ID()})
	if err != nil {
		return Rights{}, err
	}
	switch {
	case m.Owner != nil:
		return Rights{ManageTopics: true, PinMessages: true, ChangeInfo: true, DeleteMessages: true}, nil
	case m.Administrator != nil:
		a := m.Administrator
		return Rights{ManageTopics: a.CanManageTopics, PinMessages: a.CanPinMessages,
			ChangeInfo: a.CanChangeInfo, DeleteMessages: a.CanDeleteMessages}, nil
	}
	return Rights{}, nil
}

func (t *Bot) EditTopic(ctx context.Context, threadID int, name string) error {
	_, err := t.b.EditForumTopic(ctx, &bot.EditForumTopicParams{ChatID: t.chatID, MessageThreadID: threadID, Name: name})
	if notModified(err) {
		return nil
	}
	return mapErr(err)
}

func (t *Bot) CloseTopic(ctx context.Context, threadID int) error {
	_, err := t.b.CloseForumTopic(ctx, &bot.CloseForumTopicParams{ChatID: t.chatID, MessageThreadID: threadID})
	if notModified(err) {
		return nil
	}
	return mapErr(err)
}

func (t *Bot) AnswerCallback(ctx context.Context, callbackID, text string) error {
	_, err := t.b.AnswerCallbackQuery(ctx, &bot.AnswerCallbackQueryParams{CallbackQueryID: callbackID, Text: text})
	return err
}

// Me returns the bot's @username.
func (t *Bot) Me(ctx context.Context) (string, error) {
	u, err := t.b.GetMe(ctx)
	if err != nil {
		return "", err
	}
	return "@" + u.Username, nil
}

// IsForum reports whether the group has topics enabled.
func (t *Bot) IsForum(ctx context.Context) (bool, error) {
	c, err := t.b.GetChat(ctx, &bot.GetChatParams{ChatID: t.chatID})
	if err != nil {
		return false, err
	}
	return c.IsForum, nil
}

func (t *Bot) SendDocument(ctx context.Context, threadID int, name string, data []byte, caption string, silent bool) (int, error) {
	p := &bot.SendDocumentParams{
		ChatID: t.chatID, MessageThreadID: threadID,
		Document: &models.InputFileUpload{Filename: name, Data: bytes.NewReader(data)},
		Caption:  caption, ParseMode: models.ParseModeHTML, DisableNotification: silent,
	}
	m, err := t.b.SendDocument(ctx, p)
	if isParseError(err) {
		p.ParseMode, p.Caption = "", plainText(caption)
		p.Document = &models.InputFileUpload{Filename: name, Data: bytes.NewReader(data)}
		m, err = t.b.SendDocument(ctx, p)
	}
	if err != nil {
		return 0, mapErr(err)
	}
	return m.ID, nil
}

func (t *Bot) SetReaction(ctx context.Context, msgID int, emoji string) error {
	p := &bot.SetMessageReactionParams{ChatID: t.chatID, MessageID: msgID}
	if emoji != "" {
		p.Reaction = []models.ReactionType{{Type: models.ReactionTypeTypeEmoji,
			ReactionTypeEmoji: &models.ReactionTypeEmoji{Emoji: emoji}}}
	}
	_, err := t.b.SetMessageReaction(ctx, p)
	return mapErr(err)
}

func (t *Bot) DeleteMessage(ctx context.Context, msgID int) error {
	_, err := t.b.DeleteMessage(ctx, &bot.DeleteMessageParams{ChatID: t.chatID, MessageID: msgID})
	return mapErr(err)
}

// DownloadFile fetches a file the user sent (up to MaxDownload).
func (t *Bot) DownloadFile(ctx context.Context, fileID string) ([]byte, error) {
	f, err := t.b.GetFile(ctx, &bot.GetFileParams{FileID: fileID})
	if err != nil {
		return nil, mapErr(err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, t.b.FileDownloadLink(f), nil)
	if err != nil {
		return nil, err
	}
	resp, err := t.files.Do(req)
	if err != nil {
		// The URL holds the bot token: report only the cause.
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		return nil, fmt.Errorf(i18n.T("tg.download"), err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, errors.New(i18n.T("tg.download_http", resp.StatusCode))
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, MaxDownload+1))
	if err != nil {
		return nil, err
	}
	if len(data) > MaxDownload {
		return nil, errors.New(i18n.T("tg.too_big"))
	}
	return data, nil
}

func (t *Bot) SetCommands(ctx context.Context, cmds []Command) error {
	list := make([]models.BotCommand, 0, len(cmds))
	for _, c := range cmds {
		list = append(list, models.BotCommand{Command: c.Name, Description: c.Description})
	}
	_, err := t.b.SetMyCommands(ctx, &bot.SetMyCommandsParams{Commands: list, Scope: &models.BotCommandScopeChat{ChatID: t.chatID}})
	return mapErr(err)
}

// SetProfile sets the bot's short description, description and profile photo (PNG/JPEG).
func (t *Bot) SetProfile(ctx context.Context, short, description string, photo []byte) error {
	if _, err := t.b.SetMyShortDescription(ctx, &bot.SetMyShortDescriptionParams{ShortDescription: short}); err != nil {
		return fmt.Errorf(i18n.T("tg.short_desc"), mapErr(err))
	}
	if _, err := t.b.SetMyDescription(ctx, &bot.SetMyDescriptionParams{Description: description}); err != nil {
		return fmt.Errorf(i18n.T("tg.description"), mapErr(err))
	}
	if len(photo) > 0 {
		p := &models.InputProfilePhotoStatic{Photo: "attach://avatar.png", MediaAttachment: bytes.NewReader(photo)}
		if _, err := t.b.SetMyProfilePhoto(ctx, &bot.SetMyProfilePhotoParams{Photo: p}); err != nil {
			return fmt.Errorf(i18n.T("tg.photo"), mapErr(err))
		}
	}
	return nil
}
