package app

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

func (s *EventStore) JoinEvent(userID, eventID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin event join: %w", err)
	}
	defer tx.Rollback()

	var status string
	if err := tx.QueryRow("SELECT status FROM events WHERE id = ?", eventID).Scan(&status); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrEventNotFound
		}
		return fmt.Errorf("read event for join: %w", err)
	}
	if status != "published" {
		return ErrEventCancelled
	}

	result, err := tx.Exec(`
		INSERT INTO event_participants (event_id, user_id, joined_at)
		VALUES (?, ?, ?)
		ON CONFLICT (event_id, user_id) DO NOTHING
	`, eventID, userID, time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return fmt.Errorf("add event participant: %w", err)
	}
	inserted, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("check event participant insert: %w", err)
	}
	if inserted == 0 {
		return tx.Commit()
	}

	result, err = tx.Exec(`
		UPDATE events
		SET players = players + 1, spots = spots - 1
		WHERE id = ? AND status = 'published' AND spots > 0
	`, eventID)
	if err != nil {
		return fmt.Errorf("update event capacity after join: %w", err)
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("check event capacity after join: %w", err)
	}
	if updated != 1 {
		var currentStatus string
		err := tx.QueryRow("SELECT status FROM events WHERE id = ?", eventID).Scan(&currentStatus)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrEventNotFound
		}
		if err != nil {
			return fmt.Errorf("read event status after rejected join: %w", err)
		}
		if currentStatus != "published" {
			return ErrEventCancelled
		}
		return ErrEventFull
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit event join: %w", err)
	}
	return nil
}

func (s *EventStore) LeaveEvent(userID, eventID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin event leave: %w", err)
	}
	defer tx.Rollback()

	var exists int
	if err := tx.QueryRow("SELECT 1 FROM events WHERE id = ?", eventID).Scan(&exists); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrEventNotFound
		}
		return fmt.Errorf("read event for leave: %w", err)
	}
	result, err := tx.Exec("DELETE FROM event_participants WHERE event_id = ? AND user_id = ?", eventID, userID)
	if err != nil {
		return fmt.Errorf("remove event participant: %w", err)
	}
	deleted, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("check event participant removal: %w", err)
	}
	if deleted == 0 {
		return tx.Commit()
	}

	result, err = tx.Exec(`
		UPDATE events
		SET players = players - 1, spots = spots + 1
		WHERE id = ? AND players > 0
	`, eventID)
	if err != nil {
		return fmt.Errorf("restore event capacity after leave: %w", err)
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("check event capacity after leave: %w", err)
	}
	if updated != 1 {
		return fmt.Errorf("event participant count is inconsistent for %s", eventID)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit event leave: %w", err)
	}
	return nil
}

func (s *EventStore) HasJoined(userID, eventID string) (bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var exists int
	err := s.db.QueryRow(
		"SELECT 1 FROM event_participants WHERE event_id = ? AND user_id = ?",
		eventID,
		userID,
	).Scan(&exists)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}
