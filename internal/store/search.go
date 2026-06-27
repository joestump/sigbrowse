package store

import (
	"context"
	"fmt"
	"strings"

	"github.com/joestump/msgbrowse/internal/signal"
)

// Snippet highlight markers. SearchMessages wraps matched terms in these control
// characters instead of HTML so the store layer stays presentation-free; the web
// layer escapes the surrounding (untrusted) text and only then swaps these for
// <mark>…</mark>. Control characters never occur in real message bodies, so they
// are safe sentinels.
const (
	SnippetMarkStart = "\x02"
	SnippetMarkEnd   = "\x03"
)

// SearchOptions parameterizes a full-text search. Query is required; every other
// field is an optional filter (zero value = no filter).
type SearchOptions struct {
	Query          string
	ConversationID int64  // 0 = any conversation
	Source         string // "" = any source
	Sender         string // "" = any sender (exact match)
	StartUnix      int64  // 0 = no lower bound
	EndUnix        int64  // 0 = no upper bound
	HasAttachment  bool   // true = only messages with an attachment
	HasLink        bool   // true = only messages with a link
	Limit          int
}

// SearchHit is one ranked full-text match with enough provenance to render a
// result row and to jump to the message in context.
type SearchHit struct {
	MessageID        int64
	Hash             string
	ConversationID   int64
	ConversationName string
	Source           string
	Sender           string
	IsOwner          bool
	IsSystem         bool
	TS               string
	TSUnix           int64
	Snippet          string // body excerpt with SnippetMark{Start,End} around matches
	HasAttachment    bool
	HasLink          bool
}

// SearchMessages runs an FTS5 keyword search with optional filters, ranked by
// bm25 relevance. It returns at most opts.Limit hits (default 50, capped 200).
// An empty or all-whitespace query returns no hits and no error.
func (s *Store) SearchMessages(ctx context.Context, opts SearchOptions) ([]SearchHit, error) {
	match := buildFTSQuery(opts.Query)
	if match == "" {
		return nil, nil
	}
	limit := opts.Limit
	if limit <= 0 || limit > 200 {
		limit = 50
	}

	var (
		where = []string{"messages_fts MATCH ?"}
		args  = []any{match}
	)
	if opts.ConversationID > 0 {
		where = append(where, "m.conversation_id = ?")
		args = append(args, opts.ConversationID)
	}
	if opts.Source != "" {
		where = append(where, "m.source = ?")
		args = append(args, opts.Source)
	}
	if opts.Sender != "" {
		where = append(where, "m.sender = ?")
		args = append(args, opts.Sender)
	}
	if opts.StartUnix > 0 {
		where = append(where, "m.ts_unix >= ?")
		args = append(args, opts.StartUnix)
	}
	if opts.EndUnix > 0 {
		where = append(where, "m.ts_unix <= ?")
		args = append(args, opts.EndUnix)
	}
	if opts.HasAttachment {
		where = append(where, "EXISTS (SELECT 1 FROM attachments a WHERE a.message_id = m.id)")
	}
	if opts.HasLink {
		where = append(where, "EXISTS (SELECT 1 FROM links l WHERE l.message_id = m.id)")
	}

	// char(2)/char(3) are the SnippetMark sentinels; column 0 is `body`.
	q := `
SELECT m.id, m.hash, m.conversation_id, c.name, m.source, m.sender, m.is_system, m.ts, m.ts_unix,
       snippet(messages_fts, 0, char(2), char(3), '…', 12) AS snip,
       EXISTS (SELECT 1 FROM attachments a WHERE a.message_id = m.id) AS has_att,
       EXISTS (SELECT 1 FROM links l WHERE l.message_id = m.id)       AS has_link
  FROM messages_fts
  JOIN messages m      ON m.id = messages_fts.rowid
  JOIN conversations c ON c.id = m.conversation_id
 WHERE ` + strings.Join(where, "\n   AND ") + `
 ORDER BY rank
 LIMIT ?`
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("search messages: %w", err)
	}
	defer rows.Close()

	var hits []SearchHit
	for rows.Next() {
		var (
			h               SearchHit
			isSystem        int
			hasAtt, hasLink int
		)
		if err := rows.Scan(&h.MessageID, &h.Hash, &h.ConversationID, &h.ConversationName,
			&h.Source, &h.Sender, &isSystem, &h.TS, &h.TSUnix, &h.Snippet, &hasAtt, &hasLink); err != nil {
			return nil, err
		}
		h.IsSystem = isSystem == 1
		h.IsOwner = h.Sender == signal.OwnerSender
		h.HasAttachment = hasAtt == 1
		h.HasLink = hasLink == 1
		hits = append(hits, h)
	}
	return hits, rows.Err()
}

// buildFTSQuery turns free-form user input into a safe FTS5 MATCH expression.
//
// Each whitespace-separated token becomes a quoted prefix term ("foo"*), and the
// terms are ANDed (FTS5's implicit-AND of space-separated terms). Quoting every
// token neutralizes FTS5 operators and punctuation in the user's input, so the
// query can never be a syntax error or alter the query structure; embedded
// double quotes are escaped by doubling. Returns "" when the input has no usable
// tokens.
func buildFTSQuery(input string) string {
	fields := strings.Fields(input)
	if len(fields) == 0 {
		return ""
	}
	terms := make([]string, 0, len(fields))
	for _, f := range fields {
		f = strings.ReplaceAll(f, `"`, `""`)
		terms = append(terms, `"`+f+`"*`)
	}
	return strings.Join(terms, " ")
}
