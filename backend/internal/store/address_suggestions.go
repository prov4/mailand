package store

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
)

// MessageAddress is a person discovered from stored emails (From/To/Cc).
type MessageAddress struct {
	Name    string
	Address string
	LastTS  int64
}

// SearchMessageAddresses finds people from stored emails (From, To, Cc) whose
// name or email contains query (case-insensitive), excluding any lower-cased
// address in exclude. Results are deduplicated by email and ranked with prefix
// matches first, then most recently interacted with.
func (s *Store) SearchMessageAddresses(ctx context.Context, query string, limit int, exclude map[string]bool) ([]MessageAddress, error) {
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" || limit <= 0 {
		return nil, nil
	}
	like := "%" + escapeLike(q) + "%"
	byAddr := map[string]MessageAddress{}

	add := func(name, addr string, ts int64) {
		addr = strings.TrimSpace(addr)
		if addr == "" || !strings.Contains(addr, "@") {
			return
		}
		key := strings.ToLower(addr)
		if exclude[key] {
			return
		}
		name = strings.TrimSpace(name)
		if !strings.Contains(strings.ToLower(name), q) && !strings.Contains(key, q) {
			return
		}
		cur, ok := byAddr[key]
		if !ok {
			byAddr[key] = MessageAddress{Name: name, Address: addr, LastTS: ts}
			return
		}
		if ts > cur.LastTS {
			cur.LastTS = ts
			if name != "" {
				cur.Name = name
			}
		} else if cur.Name == "" && name != "" {
			cur.Name = name
		}
		byAddr[key] = cur
	}

	// 1. Senders (From). Group by (address, name) so a newer message with an
	// empty display name doesn't hide an earlier non-empty name.
	rows, err := s.db.QueryContext(ctx, `
SELECT from_name, from_address, MAX(timestamp_unix) AS last_ts
FROM messages
WHERE from_address <> ''
  AND (lower(from_name) LIKE ? ESCAPE '\' OR lower(from_address) LIKE ? ESCAPE '\')
GROUP BY lower(from_address), from_name
ORDER BY last_ts DESC
LIMIT ?`, like, like, max(limit*6, 80))
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var name, addr string
		var ts int64
		if err := rows.Scan(&name, &addr, &ts); err != nil {
			rows.Close()
			return nil, err
		}
		add(name, addr, ts)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}

	// 2. Recipients (To / Cc) from recent matching messages.
	rrows, err := s.db.QueryContext(ctx, `
SELECT to_json, cc_json, timestamp_unix
FROM messages
WHERE lower(to_json) LIKE ? ESCAPE '\' OR lower(cc_json) LIKE ? ESCAPE '\'
ORDER BY timestamp_unix DESC
LIMIT 200`, like, like)
	if err != nil {
		return nil, err
	}
	for rrows.Next() {
		var toJS, ccJS string
		var ts int64
		if err := rrows.Scan(&toJS, &ccJS, &ts); err != nil {
			rrows.Close()
			return nil, err
		}
		for _, js := range []string{toJS, ccJS} {
			if js == "" || js == "[]" {
				continue
			}
			var arr []struct{ Name, Address string }
			if json.Unmarshal([]byte(js), &arr) != nil {
				continue
			}
			for _, a := range arr {
				add(a.Name, a.Address, ts)
			}
		}
	}
	if err := rrows.Close(); err != nil {
		return nil, err
	}

	out := make([]MessageAddress, 0, len(byAddr))
	for _, a := range byAddr {
		out = append(out, a)
	}
	sort.Slice(out, func(i, j int) bool {
		pi := strings.HasPrefix(strings.ToLower(out[i].Name), q) || strings.HasPrefix(strings.ToLower(out[i].Address), q)
		pj := strings.HasPrefix(strings.ToLower(out[j].Name), q) || strings.HasPrefix(strings.ToLower(out[j].Address), q)
		if pi != pj {
			return pi
		}
		if out[i].LastTS != out[j].LastTS {
			return out[i].LastTS > out[j].LastTS
		}
		return strings.ToLower(out[i].Address) < strings.ToLower(out[j].Address)
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}
