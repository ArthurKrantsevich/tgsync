package store

import (
	"context"
	"time"
)

// TrackMessage records a control topic message for a later sweep. A message
// tracked twice keeps its first time.
func (s *Store) TrackMessage(ctx context.Context, msgID int, at time.Time) error {
	_, err := s.db.ExecContext(ctx, `INSERT OR IGNORE INTO control_msgs (msg_id, sent_at) VALUES (?, ?)`, msgID, at.Unix())
	return err
}

// TrackedMessages returns tracked messages sent before `before`, oldest first.
func (s *Store) TrackedMessages(ctx context.Context, before time.Time) ([]int, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT msg_id FROM control_msgs WHERE sent_at < ? ORDER BY sent_at, msg_id`, before.Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []int
	for rows.Next() {
		var id int
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// UntrackMessage forgets a tracked message.
func (s *Store) UntrackMessage(ctx context.Context, msgID int) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM control_msgs WHERE msg_id = ?`, msgID)
	return err
}
