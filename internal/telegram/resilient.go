package telegram

import (
	"context"
	"errors"
	"time"
)

// Resilient retries temporary Telegram failures (429, network). While
// Telegram is unreachable, calls wait instead of losing messages; the agent's
// events pile up in its buffers meanwhile.
type Resilient struct {
	API
	budget     time.Duration // total wait per call
	editBudget time.Duration // status edits are cheap to skip
	sleep      func(ctx context.Context, d time.Duration) error
}

func NewResilient(api API) *Resilient {
	return &Resilient{API: api, budget: 5 * time.Minute, editBudget: 30 * time.Second, sleep: sleepCtx}
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// do retries call on temporary errors. With create set, errors after which
// the request may have been delivered are not retried, to avoid duplicates.
func (r *Resilient) do(ctx context.Context, budget time.Duration, create bool, call func() error) error {
	var waited time.Duration
	backoff := time.Second
	for {
		err := call()
		var re *RetryError
		if !errors.As(err, &re) || (create && re.Unsafe) {
			return err
		}
		wait := re.After
		if wait == 0 {
			wait = backoff
			if backoff *= 2; backoff > 30*time.Second {
				backoff = 30 * time.Second
			}
		}
		if waited+wait > budget || r.sleep(ctx, wait) != nil {
			return err
		}
		waited += wait
	}
}

func (r *Resilient) SendMessage(ctx context.Context, threadID int, html string, kb Keyboard, silent bool) (id int, err error) {
	err = r.do(ctx, r.budget, true, func() error { id, err = r.API.SendMessage(ctx, threadID, html, kb, silent); return err })
	return id, err
}

func (r *Resilient) EditKeyboard(ctx context.Context, msgID int, kb Keyboard) error {
	return r.do(ctx, r.editBudget, false, func() error { return r.API.EditKeyboard(ctx, msgID, kb) })
}

func (r *Resilient) EditMessage(ctx context.Context, msgID int, html string, kb Keyboard) error {
	return r.do(ctx, r.editBudget, false, func() error { return r.API.EditMessage(ctx, msgID, html, kb) })
}

func (r *Resilient) SendDocument(ctx context.Context, threadID int, name string, data []byte, caption string, silent bool) (id int, err error) {
	err = r.do(ctx, r.budget, true, func() error {
		id, err = r.API.SendDocument(ctx, threadID, name, data, caption, silent)
		return err
	})
	return id, err
}

func (r *Resilient) CreateTopic(ctx context.Context, name string, color int, iconID string) (id int, err error) {
	err = r.do(ctx, r.budget, true, func() error { id, err = r.API.CreateTopic(ctx, name, color, iconID); return err })
	return id, err
}

func (r *Resilient) SetTopicIcon(ctx context.Context, threadID int, iconID string) error {
	return r.do(ctx, r.editBudget, false, func() error { return r.API.SetTopicIcon(ctx, threadID, iconID) })
}

func (r *Resilient) RemoveTopic(ctx context.Context, threadID int) error {
	return r.do(ctx, r.budget, false, func() error { return r.API.RemoveTopic(ctx, threadID) })
}

func (r *Resilient) EditTopic(ctx context.Context, threadID int, name string) error {
	return r.do(ctx, r.editBudget, false, func() error { return r.API.EditTopic(ctx, threadID, name) })
}

func (r *Resilient) CloseTopic(ctx context.Context, threadID int) error {
	return r.do(ctx, r.budget, false, func() error { return r.API.CloseTopic(ctx, threadID) })
}

func (r *Resilient) DeleteMessage(ctx context.Context, msgID int) error {
	return r.do(ctx, r.budget, false, func() error { return r.API.DeleteMessage(ctx, msgID) })
}

// AnswerCallback is not retried: Telegram expires callbacks within seconds.
