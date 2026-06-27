package store

import (
	"context"
	"testing"

	"github.com/joestump/msgbrowse/internal/source"
)

// TestConversationIdentifiers covers phone-number metadata: a conversation's
// linked contact may carry cross-source identifiers (e.g. an iMessage phone
// merged onto a Signal contact). The conversation's own name is excluded so it
// isn't echoed back as a redundant identifier.
func TestConversationIdentifiers(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	convID, err := st.UpsertConversation(ctx, source.Signal, "MJ")
	if err != nil {
		t.Fatal(err)
	}

	// Before any merge: only the bootstrap identifier (== name), so nothing extra.
	if ids, _ := st.ConversationIdentifiers(ctx, convID); len(ids) != 0 {
		t.Errorf("pre-merge identifiers = %+v, want none (name excluded)", ids)
	}

	// Simulate a contacts-page merge: add an iMessage phone handle to MJ's contact.
	var contactID int64
	if err := st.DB().QueryRow(`SELECT contact_id FROM conversations WHERE id = ?`, convID).Scan(&contactID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB().Exec(
		`INSERT INTO contact_identifiers(contact_id, source, identifier) VALUES (?, ?, ?)`,
		contactID, source.IMessage, "+15551234567"); err != nil {
		t.Fatal(err)
	}

	ids, err := st.ConversationIdentifiers(ctx, convID)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 1 || ids[0].Source != source.IMessage || ids[0].Identifier != "+15551234567" {
		t.Errorf("identifiers = %+v, want [{imessage +15551234567}]", ids)
	}
}
