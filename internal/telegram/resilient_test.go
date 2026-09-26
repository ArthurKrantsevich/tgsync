package telegram

import (
	"context"
	"errors"
	"testing"
	"time"
)

type flaky struct {
	*Fake
	fails []error
	calls int
}

func (f *flaky) SendMessage(ctx context.Context, th int, html string, kb Keyboard, silent bool) (int, error) {
	f.calls++
	if len(f.fails) > 0 {
		err := f.fails[0]
		f.fails = f.fails[1:]
		return 0, err
	}
	return f.Fake.SendMessage(ctx, th, html, kb, silent)
}

func resilient(inner API) (*Resilient, *[]time.Duration) {
	var slept []time.Duration
	r := NewResilient(inner)
	r.sleep = func(ctx context.Context, d time.Duration) error { slept = append(slept, d); return nil }
	return r, &slept
}

func TestResilientRetries(t *testing.T) {
	inner := &flaky{Fake: NewFake(), fails: []error{&RetryError{After: 7 * time.Second}, &RetryError{}}}
	r, slept := resilient(inner)
	if _, err := r.SendMessage(context.Background(), 1, "hi", nil, false); err != nil {
		t.Fatal(err)
	}
	if inner.calls != 3 || len(*slept) != 2 || (*slept)[0] != 7*time.Second || (*slept)[1] != time.Second {
		t.Fatalf("calls=%d slept=%v", inner.calls, *slept)
	}
}

func TestResilientGivesUp(t *testing.T) {
	var fails []error
	for i := 0; i < 100; i++ {
		fails = append(fails, &RetryError{})
	}
	inner := &flaky{Fake: NewFake(), fails: fails}
	r, slept := resilient(inner)
	r.budget = time.Minute
	_, err := r.SendMessage(context.Background(), 1, "hi", nil, false)
	var total time.Duration
	for _, d := range *slept {
		total += d
	}
	if err == nil || total > time.Minute {
		t.Fatalf("err=%v total wait=%s", err, total)
	}
}

func TestResilientDoesNotRetryOtherErrors(t *testing.T) {
	inner := &flaky{Fake: NewFake(), fails: []error{ErrTopicGone}}
	r, slept := resilient(inner)
	if _, err := r.SendMessage(context.Background(), 1, "hi", nil, false); !errors.Is(err, ErrTopicGone) || inner.calls != 1 || len(*slept) != 0 {
		t.Fatalf("err=%v calls=%d slept=%v", err, inner.calls, *slept)
	}
}

func TestResilientDoesNotRepeatPossiblyDeliveredSends(t *testing.T) {
	inner := &flaky{Fake: NewFake(), fails: []error{&RetryError{Unsafe: true}}}
	r, _ := resilient(inner)
	if _, err := r.SendMessage(context.Background(), 1, "hi", nil, false); err == nil || inner.calls != 1 {
		t.Fatalf("a send that may have arrived must not be repeated: err=%v calls=%d", err, inner.calls)
	}
}

type flakyCallback struct {
	*Fake
	calls int
}

func (f *flakyCallback) AnswerCallback(context.Context, string, string) error {
	f.calls++
	return &RetryError{}
}

func TestResilientDoesNotRetryCallbackAnswers(t *testing.T) {
	inner := &flakyCallback{Fake: NewFake()}
	r, _ := resilient(inner)
	_ = r.AnswerCallback(context.Background(), "cb", "")
	if inner.calls != 1 {
		t.Fatalf("callbacks expire in seconds, calls=%d", inner.calls)
	}
}
