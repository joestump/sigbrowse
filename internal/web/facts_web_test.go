package web

import (
	"context"
	"strconv"
	"strings"
	"testing"

	"github.com/joestump/msgbrowse/internal/source"
	"github.com/joestump/msgbrowse/internal/store"
)

// TestConversationShowsFacts verifies the conversation view renders the AI facts
// panel with the fact text, its category, and a jump-to-context link to the
// supporting message.
func TestConversationShowsFacts(t *testing.T) {
	srv, st, _ := newTestServer(t)
	ctx := context.Background()

	var convID, contactID int64
	if err := st.DB().QueryRow(
		`SELECT id, contact_id FROM conversations WHERE name = 'Harper'`).Scan(&convID, &contactID); err != nil {
		t.Fatalf("find Harper: %v", err)
	}
	var msgID, tsUnix int64
	var hash, ts string
	if err := st.DB().QueryRow(
		`SELECT id, hash, ts, ts_unix FROM messages WHERE conversation_id = ? ORDER BY ts_unix, id LIMIT 1`,
		convID).Scan(&msgID, &hash, &ts, &tsUnix); err != nil {
		t.Fatalf("find message: %v", err)
	}

	if _, err := st.PutFact(ctx, store.FactInput{
		ContactID: contactID, Fact: "Has a dog named Biscuit", Category: "personal",
		Source: source.Signal, SourceMessageHash: hash, SourceTS: ts, SourceTSUnix: tsUnix, Model: "test",
	}); err != nil {
		t.Fatalf("put fact: %v", err)
	}

	body := get(t, srv, "/c/"+strconv.FormatInt(convID, 10)).Body.String()
	wantLink := "/c/" + strconv.FormatInt(convID, 10) + "/at/" + strconv.FormatInt(msgID, 10)
	for _, want := range []string{"What the AI has learned", "Has a dog named Biscuit", "personal", wantLink} {
		if !strings.Contains(body, want) {
			t.Errorf("conversation page missing %q", want)
		}
	}
}

// TestConversationNoFactsPanelWhenEmpty ensures the panel is absent for a
// contact with no extracted facts (no empty card noise).
func TestConversationNoFactsPanelWhenEmpty(t *testing.T) {
	srv, st, _ := newTestServer(t)
	var convID int64
	if err := st.DB().QueryRow(`SELECT id FROM conversations WHERE name = 'Harper'`).Scan(&convID); err != nil {
		t.Fatalf("find Harper: %v", err)
	}
	body := get(t, srv, "/c/"+strconv.FormatInt(convID, 10)).Body.String()
	if strings.Contains(body, "What the AI has learned") {
		t.Error("facts panel rendered for a contact with no facts")
	}
}
