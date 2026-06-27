package store

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/joestump/msgbrowse/internal/signal"
	"github.com/joestump/msgbrowse/internal/source"
)

// seedSearchCorpus populates a store with a small known corpus across two
// conversations and returns it. Messages are chosen to exercise every filter.
func seedSearchCorpus(t *testing.T) *Store {
	t.Helper()
	st := newTestStore(t)
	ctx := context.Background()

	harper, err := st.UpsertConversation(ctx, source.Signal, "Harper")
	if err != nil {
		t.Fatal(err)
	}
	group, err := st.UpsertConversation(ctx, source.Signal, "Group Trip")
	if err != nil {
		t.Fatal(err)
	}

	harperMsgs := []signal.Message{
		msg("Harper", "2022-03-01 09:00:00", "Harper", "morning ready for the trip", nil, nil),
		msg("Harper", "2022-03-01 09:01:00", "Me", "packing now", nil, nil),
		msg("Harper", "2022-03-01 09:03:00", "Harper", "the lease agreement is attached",
			[]signal.Attachment{{Kind: signal.KindFile, RelPath: "media/lease.pdf", OriginalName: "lease.pdf"}}, nil),
		msg("Harper", "2022-03-01 09:04:00", "Me", "see the map at maps example com",
			nil, []signal.Link{{URL: "https://maps.example.com/cabin"}}),
	}
	if _, err := st.ReplaceConversationMessages(ctx, harper, source.Signal, harperMsgs); err != nil {
		t.Fatal(err)
	}

	groupMsgs := []signal.Message{
		msg("Group Trip", "2022-04-02 18:03:00", "MJ", "a few notes on the lease terms", nil, nil),
		msg("Group Trip", "2022-04-02 18:04:00", "Me", "book it", nil, nil),
	}
	if _, err := st.ReplaceConversationMessages(ctx, group, source.Signal, groupMsgs); err != nil {
		t.Fatal(err)
	}
	return st
}

func TestSearchMessagesBasic(t *testing.T) {
	st := seedSearchCorpus(t)
	hits, err := st.SearchMessages(context.Background(), SearchOptions{Query: "trip"})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 {
		t.Fatalf("got %d hits, want 1: %+v", len(hits), hits)
	}
	h := hits[0]
	if h.ConversationName != "Harper" || h.Sender != "Harper" {
		t.Errorf("hit provenance wrong: %+v", h)
	}
	if h.Source != source.Signal {
		t.Errorf("hit source = %q, want signal", h.Source)
	}
	// Snippet wraps the matched term in the control-char sentinels.
	if !strings.Contains(h.Snippet, SnippetMarkStart) || !strings.Contains(h.Snippet, SnippetMarkEnd) {
		t.Errorf("snippet missing highlight markers: %q", h.Snippet)
	}
}

func TestSearchMessagesPrefix(t *testing.T) {
	st := seedSearchCorpus(t)
	// "lea" should prefix-match "lease" in two conversations.
	hits, err := st.SearchMessages(context.Background(), SearchOptions{Query: "lea"})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 2 {
		t.Fatalf("prefix 'lea' got %d hits, want 2", len(hits))
	}
}

func TestSearchMessagesFilters(t *testing.T) {
	st := seedSearchCorpus(t)
	ctx := context.Background()

	// Resolve conversation ids by name for filter assertions.
	convs, err := st.ListConversations(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var harperID int64
	for _, c := range convs {
		if c.Name == "Harper" {
			harperID = c.ID
		}
	}

	tests := []struct {
		name string
		opts SearchOptions
		want int
	}{
		{"conversation filter", SearchOptions{Query: "lease", ConversationID: harperID}, 1},
		{"sender filter", SearchOptions{Query: "lease", Sender: "MJ"}, 1},
		{"has attachment", SearchOptions{Query: "lease", HasAttachment: true}, 1},
		{"has link excludes plain", SearchOptions{Query: "lease", HasLink: true}, 0},
		{"has link matches", SearchOptions{Query: "maps", HasLink: true}, 1},
		{"source signal", SearchOptions{Query: "lease", Source: source.Signal}, 2},
		{"source imessage none", SearchOptions{Query: "lease", Source: source.IMessage}, 0},
		// Harper's "lease" is 2022-03-01; Group's is 2022-04-02.
		{"date lower bound keeps only april", SearchOptions{Query: "lease", StartUnix: dayUnix(t, "2022-03-15")}, 1},
		{"date upper bound keeps only march", SearchOptions{Query: "lease", EndUnix: dayUnix(t, "2022-03-31")}, 1},
		{"date range includes march trip", SearchOptions{Query: "trip", StartUnix: dayUnix(t, "2022-02-01"), EndUnix: dayUnix(t, "2022-03-31")}, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hits, err := st.SearchMessages(ctx, tt.opts)
			if err != nil {
				t.Fatal(err)
			}
			if len(hits) != tt.want {
				t.Errorf("got %d hits, want %d: %+v", len(hits), tt.want, hits)
			}
		})
	}
}

// TestSearchInjectionSafe confirms that FTS5 operators / punctuation in the user
// query never cause a syntax error — buildFTSQuery quotes every token.
func TestSearchInjectionSafe(t *testing.T) {
	st := seedSearchCorpus(t)
	ctx := context.Background()
	for _, q := range []string{`"`, `lease OR`, `lease)`, `*`, `NEAR(`, `^foo`, `""`, `  `, `a"b`} {
		if _, err := st.SearchMessages(ctx, SearchOptions{Query: q}); err != nil {
			t.Errorf("query %q returned error: %v", q, err)
		}
	}
}

func TestSearchEmptyQuery(t *testing.T) {
	st := seedSearchCorpus(t)
	hits, err := st.SearchMessages(context.Background(), SearchOptions{Query: "   "})
	if err != nil {
		t.Fatal(err)
	}
	if hits != nil {
		t.Errorf("empty query should return nil hits, got %+v", hits)
	}
}

func TestBuildFTSQuery(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"", ""},
		{"   ", ""},
		{"lease", `"lease"*`},
		{"lease terms", `"lease"* "terms"*`},
		{`a"b`, `"a""b"*`},
		{"OR AND", `"OR"* "AND"*`}, // operators become literal quoted terms
	}
	for _, tt := range tests {
		if got := buildFTSQuery(tt.in); got != tt.want {
			t.Errorf("buildFTSQuery(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

// dayUnix parses a YYYY-MM-DD into a UTC unix second for date-filter tests.
func dayUnix(t *testing.T, date string) int64 {
	t.Helper()
	parsed, err := time.Parse("2006-01-02", date)
	if err != nil {
		t.Fatalf("parse %q: %v", date, err)
	}
	return parsed.UTC().Unix()
}
