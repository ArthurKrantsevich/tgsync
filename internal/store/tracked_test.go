package store

import (
	"context"
	"reflect"
	"testing"
	"time"
)

func TestTrackedMessages(t *testing.T) {
	s, ctx := open(t), context.Background()
	now := time.Now()
	for id, age := range map[int]time.Duration{1: 2 * time.Hour, 2: 3 * time.Hour, 3: time.Minute} {
		if err := s.TrackMessage(ctx, id, now.Add(-age)); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.TrackMessage(ctx, 1, now); err != nil { // tracked twice: first time kept
		t.Fatal(err)
	}
	ids, err := s.TrackedMessages(ctx, now.Add(-time.Hour))
	if err != nil || !reflect.DeepEqual(ids, []int{2, 1}) {
		t.Fatalf("old: %v %v", ids, err)
	}
	if err := s.UntrackMessage(ctx, 2); err != nil {
		t.Fatal(err)
	}
	if ids, _ := s.TrackedMessages(ctx, now.Add(time.Second)); !reflect.DeepEqual(ids, []int{1, 3}) {
		t.Fatalf("all: %v", ids)
	}
}
