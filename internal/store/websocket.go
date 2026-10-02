package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

type WSConnection struct {
	ID                int64      `json:"id"`
	URL               string     `json:"url"`
	InScope           bool       `json:"inScope"`
	OpenedAt          time.Time  `json:"openedAt"`
	ClosedAt          *time.Time `json:"closedAt"`
	State             string     `json:"state"`
	Gaps              int64      `json:"gaps"`
	CaptureIncomplete bool       `json:"captureIncomplete"`
}

type WSMessage struct {
	ID           int64     `json:"id"`
	ConnectionID int64     `json:"connectionId"`
	Sequence     int64     `json:"sequence"`
	Direction    string    `json:"direction"`
	ObservedAt   time.Time `json:"observedAt"`
	Type         string    `json:"type"`
	Size         int64     `json:"size"`
	Payload      []byte    `json:"-"`
	Truncated    bool      `json:"truncated"`
	Complete     bool      `json:"complete"`
	Encoding     string    `json:"encoding"`
}

type WSPageRequest struct {
	BeforeID   int64 `json:"beforeId"`
	SnapshotID int64 `json:"snapshotId"`
}

type WSConnectionsPage struct {
	Items        []WSConnection `json:"items"`
	NextBeforeID int64          `json:"nextBeforeId"`
	SnapshotID   int64          `json:"snapshotId"`
}

type WSMessagesPage struct {
	Items        []WSMessage `json:"items"`
	NextBeforeID int64       `json:"nextBeforeId"`
	SnapshotID   int64       `json:"snapshotId"`
}

type WebSocketStore interface {
	SaveWSConnection(context.Context, *WSConnection) error
	SaveWSMessage(context.Context, *WSMessage) error
	FinishWSConnection(context.Context, int64, string, int64, bool) error
	ListWSConnections(context.Context, WSPageRequest) (WSConnectionsPage, error)
	GetWSConnection(context.Context, int64) (*WSConnection, error)
	ListWSMessages(context.Context, int64, WSPageRequest) (WSMessagesPage, error)
	GetWSMessage(context.Context, int64, int64) (*WSMessage, error)
}

var _ WebSocketStore = (*SQLiteStore)(nil)

// Recover only at application startup, not whenever another database handle opens.
func (s *SQLiteStore) RecoverWSConnections(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `UPDATE ws_connections SET state='interrupted', closed_at_unix_nano=?, capture_incomplete=1 WHERE state='open'`, time.Now().UTC().UnixNano())
	return err
}

func (s *SQLiteStore) UpdateWSCapture(ctx context.Context, id, gaps int64, incomplete bool) error {
	if id <= 0 || gaps < 0 {
		return fmt.Errorf("invalid websocket capture update")
	}
	result, err := s.db.ExecContext(ctx, `UPDATE ws_connections SET gaps=MAX(gaps,?),capture_incomplete=MAX(capture_incomplete,?) WHERE id=?`, gaps, incomplete, id)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err == nil && n == 0 {
		return sql.ErrNoRows
	}
	return err
}

func (s *SQLiteStore) SaveWSConnection(ctx context.Context, c *WSConnection) error {
	if c == nil || c.ID != 0 || len(c.URL) > 65536 || c.Gaps < 0 || c.ClosedAt != nil || (c.State != "" && c.State != "open") {
		return fmt.Errorf("invalid websocket connection")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// Terminal state, timestamp and gap counters are covered by the fixed allowance.
	charge := CaptureRecordAllowance + int64(len(c.URL))
	if err := reserveCapture(ctx, tx, charge); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO ws_connections
 (url,in_scope,opened_at_unix_nano,state,gaps,capture_incomplete,capture_bytes)
 VALUES (?,?,?,'open',?,?,?)`, c.URL, c.InScope, c.OpenedAt.UnixNano(), c.Gaps, c.CaptureIncomplete, charge)
	if err != nil {
		return fmt.Errorf("save websocket connection: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	c.ID = id
	c.State = "open"
	return nil
}

func (s *SQLiteStore) SaveWSMessage(ctx context.Context, m *WSMessage) error {
	if m == nil || m.ID != 0 || m.ConnectionID <= 0 || m.Sequence < 0 || m.Size < 0 || m.Size < int64(len(m.Payload)) || len(m.Direction) == 0 || len(m.Direction) > 64 || len(m.Type) == 0 || len(m.Type) > 32 || len(m.Encoding) > 64 {
		return fmt.Errorf("invalid websocket message")
	}
	payload, truncated := s.capBody(m.Payload)
	truncated = truncated || m.Truncated || m.Size > int64(len(payload))
	charge := CaptureRecordAllowance + captureTextBytes(m.Direction, m.Type, m.Encoding) + int64(len(payload))
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := reserveCapture(ctx, tx, charge); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO ws_messages
 (connection_id,sequence,direction,observed_at_unix_nano,type,size,payload,truncated,complete,encoding,capture_bytes)
 VALUES (?,?,?,?,?,?,?,?,?,?,?)`, m.ConnectionID, m.Sequence, m.Direction, m.ObservedAt.UnixNano(), m.Type, m.Size, payload, truncated, m.Complete, m.Encoding, charge)
	if err != nil {
		return fmt.Errorf("save websocket message: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	m.ID = id
	m.Truncated = truncated
	return nil
}

func (s *SQLiteStore) FinishWSConnection(ctx context.Context, id int64, state string, gaps int64, incomplete bool) error {
	if id <= 0 || gaps < 0 || (state != "closed" && state != "interrupted") {
		return fmt.Errorf("invalid websocket completion")
	}
	result, err := s.db.ExecContext(ctx, `UPDATE ws_connections SET
 state = CASE WHEN state = 'open' THEN ? ELSE state END,
 closed_at_unix_nano = COALESCE(closed_at_unix_nano, ?),
 gaps = MAX(gaps, ?), capture_incomplete = MAX(capture_incomplete, ?)
 WHERE id = ?`, state, time.Now().UTC().UnixNano(), gaps, incomplete, id)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

const wsConnectionColumns = `id,url,in_scope,opened_at_unix_nano,closed_at_unix_nano,state,gaps,capture_incomplete`
const wsMessageColumns = `id,connection_id,sequence,direction,observed_at_unix_nano,type,size,truncated,complete,encoding`

type wsScanner interface{ Scan(...any) error }

func scanWSConnection(row wsScanner) (*WSConnection, error) {
	c := &WSConnection{}
	var opened int64
	var closed sql.NullInt64
	if err := row.Scan(&c.ID, &c.URL, &c.InScope, &opened, &closed, &c.State, &c.Gaps, &c.CaptureIncomplete); err != nil {
		return nil, err
	}
	c.OpenedAt = time.Unix(0, opened).UTC()
	if closed.Valid {
		at := time.Unix(0, closed.Int64).UTC()
		c.ClosedAt = &at
	}
	return c, nil
}

func scanWSMessage(row wsScanner, detail bool) (*WSMessage, error) {
	m := &WSMessage{}
	var observed, retained int64
	args := []any{&m.ID, &m.ConnectionID, &m.Sequence, &m.Direction, &observed, &m.Type, &m.Size, &m.Truncated, &m.Complete, &m.Encoding}
	if detail {
		args = append(args, &m.Payload, &retained)
	}
	if err := row.Scan(args...); err != nil {
		return nil, err
	}
	m.ObservedAt = time.Unix(0, observed).UTC()
	if detail && retained > int64(len(m.Payload)) {
		m.Truncated = true
	}
	return m, nil
}

func (s *SQLiteStore) GetWSConnection(ctx context.Context, id int64) (*WSConnection, error) {
	if id <= 0 {
		return nil, fmt.Errorf("invalid websocket connection id")
	}
	return scanWSConnection(s.db.QueryRowContext(ctx, `SELECT `+wsConnectionColumns+` FROM ws_connections WHERE id = ?`, id))
}

func (s *SQLiteStore) GetWSMessage(ctx context.Context, connectionID, id int64) (*WSMessage, error) {
	if connectionID <= 0 || id <= 0 {
		return nil, fmt.Errorf("invalid websocket message id")
	}
	// Slice in SQLite, not after loading a potentially larger historical BLOB.
	return scanWSMessage(s.db.QueryRowContext(ctx, `SELECT `+wsMessageColumns+`,
 substr(CAST(payload AS BLOB),1,?),COALESCE(length(CAST(payload AS BLOB)),0)
 FROM ws_messages WHERE connection_id = ? AND id = ?`, s.bodyLimitBytes, connectionID, id), true)
}

func validateWSPageRequest(r WSPageRequest) error {
	if r.BeforeID < 0 || r.SnapshotID < 0 || (r.BeforeID > 0 && (r.SnapshotID == 0 || r.BeforeID > r.SnapshotID)) {
		return fmt.Errorf("invalid websocket page cursors")
	}
	return nil
}

func (s *SQLiteStore) ListWSConnections(ctx context.Context, r WSPageRequest) (WSConnectionsPage, error) {
	p := WSConnectionsPage{Items: make([]WSConnection, 0, 100), SnapshotID: r.SnapshotID}
	if err := validateWSPageRequest(r); err != nil {
		return p, err
	}
	if p.SnapshotID == 0 {
		if err := s.db.QueryRowContext(ctx, `SELECT COALESCE(MAX(id),0) FROM ws_connections`).Scan(&p.SnapshotID); err != nil {
			return p, err
		}
	}
	query := `SELECT ` + wsConnectionColumns + ` FROM ws_connections WHERE id <= ?`
	args := []any{p.SnapshotID}
	if r.BeforeID > 0 {
		query += ` AND id < ?`
		args = append(args, r.BeforeID)
	}
	rows, err := s.db.QueryContext(ctx, query+` ORDER BY id DESC LIMIT 101`, args...)
	if err != nil {
		return p, err
	}
	defer rows.Close()
	for rows.Next() {
		c, err := scanWSConnection(rows)
		if err != nil {
			return p, err
		}
		if len(p.Items) == 100 {
			p.NextBeforeID = p.Items[99].ID
			break
		}
		p.Items = append(p.Items, *c)
	}
	return p, rows.Err()
}

func (s *SQLiteStore) ListWSMessages(ctx context.Context, connectionID int64, r WSPageRequest) (WSMessagesPage, error) {
	p := WSMessagesPage{Items: make([]WSMessage, 0, 100), SnapshotID: r.SnapshotID}
	if err := validateWSPageRequest(r); err != nil {
		return p, err
	}
	if _, err := s.GetWSConnection(ctx, connectionID); err != nil {
		return p, err
	}
	if p.SnapshotID == 0 {
		if err := s.db.QueryRowContext(ctx, `SELECT COALESCE(MAX(id),0) FROM ws_messages WHERE connection_id = ?`, connectionID).Scan(&p.SnapshotID); err != nil {
			return p, err
		}
	}
	query := `SELECT ` + wsMessageColumns + ` FROM ws_messages WHERE connection_id = ? AND id <= ?`
	args := []any{connectionID, p.SnapshotID}
	if r.BeforeID > 0 {
		query += ` AND id < ?`
		args = append(args, r.BeforeID)
	}
	rows, err := s.db.QueryContext(ctx, query+` ORDER BY id DESC LIMIT 101`, args...)
	if err != nil {
		return p, err
	}
	defer rows.Close()
	for rows.Next() {
		m, err := scanWSMessage(rows, false)
		if err != nil {
			return p, err
		}
		if len(p.Items) == 100 {
			p.NextBeforeID = p.Items[99].ID
			break
		}
		p.Items = append(p.Items, *m)
	}
	return p, rows.Err()
}
