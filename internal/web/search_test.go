package web

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/joestump/msgbrowse/internal/signal"
	"github.com/joestump/msgbrowse/internal/source"
	"github.com/joestump/msgbrowse/internal/store"
)

func TestSearchLiveResults(t *testing.T) {
	srv, st, _ := newTestServer(t)
	conv, _ := st.GetConversation(context.Background(), "Harper")

	rec := get(t, srv, "/search/results?q=lease")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	body := rec.Body.String()
	// Matched term is highlighted and the result links into jump-to-context.
	if !contains(body, "<mark>") {
		t.Errorf("no <mark> highlight in results: %s", body)
	}
	if !contains(body, "/c/"+itoa(conv.ID)+"/at/") {
		t.Errorf("result missing jump-to-context link")
	}
	if !contains(body, "Harper") {
		t.Errorf("result missing conversation name")
	}
}

func TestSearchEmptyQueryShowsHint(t *testing.T) {
	srv, _, _ := newTestServer(t)
	rec := get(t, srv, "/search/results")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if body := rec.Body.String(); !contains(body, "Type a query") {
		t.Errorf("empty query should show hint, got: %s", body)
	}
}

func TestSearchInjectionNo500(t *testing.T) {
	srv, _, _ := newTestServer(t)
	// FTS operators / unbalanced quotes must not produce a 500.
	for _, q := range []string{"%22", "lease)", "NEAR(", "*", "lease+OR"} {
		rec := get(t, srv, "/search/results?q="+q)
		if rec.Code != http.StatusOK {
			t.Errorf("q=%q status = %d, want 200", q, rec.Code)
		}
	}
}

// TestSearchHighlightEscapesHTML is the security-critical test: a message body
// containing HTML must be escaped in the snippet, with only the highlight marks
// becoming real markup. We seed directly so the body is fully controlled.
func TestSearchHighlightEscapesHTML(t *testing.T) {
	srv, st, _ := newTestServer(t)
	ctx := context.Background()
	id, err := st.UpsertConversation(ctx, source.Signal, "XSS")
	if err != nil {
		t.Fatal(err)
	}
	parsed, _ := time.Parse(signal.TimestampLayout, "2022-05-01 10:00:00")
	_, err = st.ReplaceConversationMessages(ctx, id, source.Signal, []signal.Message{
		{Conversation: "XSS", Timestamp: parsed, TimestampRaw: "2022-05-01 10:00:00",
			Sender: "Mallory", Body: `<script>alert(1)</script> exploitword`},
	})
	if err != nil {
		t.Fatal(err)
	}

	rec := get(t, srv, "/search/results?q=exploitword")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	body := rec.Body.String()
	if contains(body, "<script>alert(1)</script>") {
		t.Errorf("raw <script> leaked into search results (XSS): %s", body)
	}
	if !contains(body, "&lt;script&gt;") {
		t.Errorf("script tag was not HTML-escaped in snippet")
	}
}

func TestConversationAtJump(t *testing.T) {
	srv, st, _ := newTestServer(t)
	ctx := context.Background()
	conv, _ := st.GetConversation(ctx, "Harper")

	// Find a real message id in this conversation via search.
	hits, err := st.SearchMessages(ctx, store.SearchOptions{Query: "lease", ConversationID: conv.ID})
	if err != nil || len(hits) == 0 {
		t.Fatalf("seed search failed: %v (%d hits)", err, len(hits))
	}
	mid := hits[0].MessageID

	rec := get(t, srv, "/c/"+itoa(conv.ID)+"/at/"+itoa(mid))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	body := rec.Body.String()
	if !contains(body, `id="m`+itoa(mid)+`"`) {
		t.Errorf("jump view missing anchor for message %d", mid)
	}
	if !contains(body, "target") {
		t.Errorf("jump view does not mark the target message")
	}
}

func TestConversationAtNotFound(t *testing.T) {
	srv, st, _ := newTestServer(t)
	conv, _ := st.GetConversation(context.Background(), "Harper")
	// Unknown message id -> 404.
	if rec := get(t, srv, "/c/"+itoa(conv.ID)+"/at/999999"); rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}
