package store

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

const findingsPageSize = 100

type FindingSummary struct {
	Type             string `json:"type"`
	Host             string `json:"host"`
	Subject          string `json:"subject"`
	Count            int64  `json:"count"`
	LatestExchangeID int64  `json:"latestExchangeId"`
	LatestPath       string `json:"latestPath"`
}

type FindingsRequest struct {
	Host, Type string
	Offset     int
	SnapshotID int64
}

type FindingsPage struct {
	Items      []FindingSummary `json:"items"`
	NextOffset int              `json:"nextOffset"`
	SnapshotID int64            `json:"snapshotId"`
}

type PassiveFindingsStore interface {
	ListPassiveFindings(context.Context, FindingsRequest) (FindingsPage, error)
}

func passiveHeaderValues(headers map[string][]string, name string) []string {
	var values []string
	for key, entries := range headers {
		if strings.EqualFold(key, name) {
			values = append(values, entries...)
		}
	}
	return values
}

func passiveTypes(scheme, mime string, headers map[string][]string) []struct{ kind, subject string } {
	var found []struct{ kind, subject string }
	if strings.EqualFold(scheme, "https") {
		if len(passiveHeaderValues(headers, "Strict-Transport-Security")) == 0 {
			found = append(found, struct{ kind, subject string }{"hsts_missing", ""})
		}
		for _, cookie := range passiveHeaderValues(headers, "Set-Cookie") {
			first, _, _ := strings.Cut(cookie, ";")
			name, _, ok := strings.Cut(first, "=")
			name = strings.TrimSpace(name)
			if !ok || name == "" || strings.ContainsAny(name, " \t\r\n;,\x7f") {
				continue
			}
			secure := false
			parts := strings.Split(cookie, ";")
			for _, part := range parts[1:] {
				if strings.EqualFold(strings.TrimSpace(part), "Secure") {
					secure = true
					break
				}
			}
			if !secure {
				found = append(found, struct{ kind, subject string }{"cookie_secure_missing", name})
			}
		}
	}
	if mime == "" {
		values := passiveHeaderValues(headers, "Content-Type")
		if len(values) > 0 {
			mime = values[0]
		}
	}
	if strings.HasPrefix(strings.ToLower(mime), "text/html") && len(passiveHeaderValues(headers, "Content-Security-Policy")) == 0 {
		found = append(found, struct{ kind, subject string }{"csp_missing", ""})
	}
	return found
}

func (s *SQLiteStore) ListPassiveFindings(ctx context.Context, request FindingsRequest) (FindingsPage, error) {
	if request.Offset < 0 || request.SnapshotID < 0 {
		return FindingsPage{}, fmt.Errorf("invalid findings cursor")
	}
	snapshot := request.SnapshotID
	if snapshot == 0 {
		if err := s.db.QueryRowContext(ctx, `SELECT COALESCE(MAX(id),0) FROM exchanges WHERE project_id=1`).Scan(&snapshot); err != nil {
			return FindingsPage{}, err
		}
	}
	query := `SELECT e.id,e.host,e.path,e.scheme,e.mime_type,b.response_headers_json FROM exchanges e JOIN exchange_bodies b ON b.exchange_id=e.id WHERE e.project_id=1 AND e.id<=? AND e.in_scope=1 AND e.error=0 AND e.status BETWEEN 200 AND 399`
	args := []any{snapshot}
	if request.Host != "" {
		query += ` AND e.host=?`
		args = append(args, request.Host)
	}
	query += ` ORDER BY e.id DESC`
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return FindingsPage{}, err
	}
	defer rows.Close()
	type key struct{ host, kind, subject string }
	groups := make(map[key]*FindingSummary)
	for rows.Next() {
		var id int64
		var host, path, scheme, mime, headerJSON string
		if err := rows.Scan(&id, &host, &path, &scheme, &mime, &headerJSON); err != nil {
			return FindingsPage{}, err
		}
		var headers map[string][]string
		if err := json.Unmarshal([]byte(headerJSON), &headers); err != nil {
			return FindingsPage{}, fmt.Errorf("decode response headers for exchange %d: %w", id, err)
		}
		seen := make(map[key]bool)
		for _, issue := range passiveTypes(scheme, mime, headers) {
			if request.Type != "" && request.Type != issue.kind {
				continue
			}
			k := key{host, issue.kind, issue.subject}
			if seen[k] {
				continue
			}
			seen[k] = true
			if existing, ok := groups[k]; ok {
				existing.Count++
			} else {
				groups[k] = &FindingSummary{Type: issue.kind, Host: host, Subject: issue.subject, Count: 1, LatestExchangeID: id, LatestPath: path}
			}
		}
	}
	if err := rows.Err(); err != nil {
		return FindingsPage{}, err
	}
	items := make([]FindingSummary, 0, len(groups))
	for _, item := range groups {
		items = append(items, *item)
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].LatestExchangeID != items[j].LatestExchangeID {
			return items[i].LatestExchangeID > items[j].LatestExchangeID
		}
		if items[i].Host != items[j].Host {
			return items[i].Host < items[j].Host
		}
		if items[i].Type != items[j].Type {
			return items[i].Type < items[j].Type
		}
		return items[i].Subject < items[j].Subject
	})
	page := FindingsPage{Items: []FindingSummary{}, SnapshotID: snapshot}
	if request.Offset >= len(items) {
		return page, nil
	}
	end := request.Offset + findingsPageSize
	if end < len(items) {
		page.NextOffset = end
	} else {
		end = len(items)
	}
	page.Items = items[request.Offset:end]
	return page, nil
}
