package db

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"time"
)

const sessionTTL = 30 * 24 * time.Hour // 30 days

type Session struct {
	Token     string
	UserID    string
	CreatedAt int64
	ExpiresAt int64
}

type SessionStore struct {
	rdb *sql.DB
	wdb *ResilientWriter
}

func NewSessionStore(p *DBPair) *SessionStore {
	return &SessionStore{rdb: p.ReadDB, wdb: p.WriteDB}
}

func (s *SessionStore) Create(userID string) (*Session, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return nil, err
	}
	sess := &Session{
		Token:     hex.EncodeToString(b),
		UserID:    userID,
		CreatedAt: time.Now().Unix(),
		ExpiresAt: time.Now().Add(sessionTTL).Unix(),
	}
	_, err := s.wdb.Exec(
		`INSERT INTO sessions (token, user_id, created_at, expires_at) VALUES (?, ?, ?, ?)`,
		sess.Token, sess.UserID, sess.CreatedAt, sess.ExpiresAt,
	)
	return sess, err
}

// GetValid returns a session only if it exists and has not expired.
func (s *SessionStore) GetValid(token string) (*Session, error) {
	sess := &Session{}
	err := s.rdb.QueryRow(
		`SELECT token, user_id, created_at, expires_at FROM sessions
		 WHERE token = ? AND expires_at > ?`,
		token, time.Now().Unix(),
	).Scan(&sess.Token, &sess.UserID, &sess.CreatedAt, &sess.ExpiresAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return sess, err
}

func (s *SessionStore) Delete(token string) error {
	_, err := s.wdb.Exec(`DELETE FROM sessions WHERE token = ?`, token)
	return err
}

// DeleteExpired removes all expired sessions (call periodically or on startup).
func (s *SessionStore) DeleteExpired() error {
	_, err := s.wdb.Exec(`DELETE FROM sessions WHERE expires_at <= ?`, time.Now().Unix())
	return err
}
