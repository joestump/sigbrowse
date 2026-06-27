package store

import (
	"context"
	"testing"

	"github.com/joestump/msgbrowse/internal/signal"
	"github.com/joestump/msgbrowse/internal/source"
)

// firstMessage returns the hash, id, ts and ts_unix of a conversation's earliest
// message — provenance values a fact would carry.
func firstMessage(t *testing.T, st *Store, convID int64) (hash, ts string, id, tsUnix int64) {
	t.Helper()
	err := st.DB().QueryRow(
		`SELECT hash, id, ts, ts_unix FROM messages WHERE conversation_id = ? ORDER BY ts_unix, id LIMIT 1`,
		convID).Scan(&hash, &id, &ts, &tsUnix)
	if err != nil {
		t.Fatalf("firstMessage: %v", err)
	}
	return hash, ts, id, tsUnix
}

func contactID(t *testing.T, st *Store, convID int64) int64 {
	t.Helper()
	var cid int64
	if err := st.DB().QueryRow(`SELECT contact_id FROM conversations WHERE id = ?`, convID).Scan(&cid); err != nil {
		t.Fatalf("contactID: %v", err)
	}
	return cid
}

func seedConversation(t *testing.T, st *Store, src, name string) int64 {
	t.Helper()
	ctx := context.Background()
	id, err := st.UpsertConversation(ctx, src, name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.ReplaceConversationMessages(ctx, id, src, []signal.Message{
		msg(name, "2023-05-01 10:00:00", name, "i just adopted a dog named Biscuit", nil, nil),
		msg(name, "2023-05-01 10:01:00", signal.OwnerSender, "aww congrats", nil, nil),
	}); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestPutFactDedupAndProvenance(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	conv := seedConversation(t, st, source.Signal, "Harper")
	cid := contactID(t, st, conv)
	hash, ts, msgID, tsUnix := firstMessage(t, st, conv)

	in := FactInput{
		ContactID: cid, Fact: "Has a dog named Biscuit", Category: "personal",
		Source: source.Signal, SourceMessageHash: hash, SourceTS: ts, SourceTSUnix: tsUnix,
		Model: "test-model",
	}
	added, err := st.PutFact(ctx, in)
	if err != nil || !added {
		t.Fatalf("first PutFact added=%v err=%v, want true,nil", added, err)
	}
	// Same fact (different casing/spacing) must dedup.
	in2 := in
	in2.Fact = "  has a DOG named Biscuit  "
	added, err = st.PutFact(ctx, in2)
	if err != nil || added {
		t.Fatalf("dup PutFact added=%v err=%v, want false,nil", added, err)
	}

	facts, err := st.ContactFactsByConversation(ctx, conv)
	if err != nil {
		t.Fatal(err)
	}
	if len(facts) != 1 {
		t.Fatalf("got %d facts, want 1", len(facts))
	}
	got := facts[0]
	if got.Fact != "Has a dog named Biscuit" || got.Category != "personal" {
		t.Errorf("fact = %+v, want trimmed original text/category", got)
	}
	if got.SourceMessageID != msgID {
		t.Errorf("SourceMessageID = %d, want resolved %d", got.SourceMessageID, msgID)
	}
}

func TestContactFactsProvenanceUnresolvedAfterReingest(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	conv := seedConversation(t, st, source.Signal, "Harper")
	cid := contactID(t, st, conv)
	hash, ts, _, tsUnix := firstMessage(t, st, conv)

	if _, err := st.PutFact(ctx, FactInput{
		ContactID: cid, Fact: "Likes hiking", Category: "preferences",
		Source: source.Signal, SourceMessageHash: hash, SourceTS: ts, SourceTSUnix: tsUnix, Model: "m",
	}); err != nil {
		t.Fatal(err)
	}
	// A fact whose supporting message hash no longer exists resolves to id 0
	// rather than failing — the UI then renders it without a jump link.
	if _, err := st.DB().Exec(`DELETE FROM messages WHERE hash = ?`, hash); err != nil {
		t.Fatal(err)
	}
	facts, err := st.ContactFactsByConversation(ctx, conv)
	if err != nil {
		t.Fatal(err)
	}
	if len(facts) != 1 || facts[0].SourceMessageID != 0 {
		t.Fatalf("facts = %+v, want one fact with SourceMessageID 0", facts)
	}
}

func TestFactConversationsExcludesDenylistContactlessAndEmpty(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	keep := seedConversation(t, st, source.Signal, "Harper")
	secret := seedConversation(t, st, source.Signal, "Secret")

	// Contactless conversation (e.g. a group) — not eligible.
	groupID := seedConversation(t, st, source.Signal, "GroupChat")
	if _, err := st.DB().Exec(`UPDATE conversations SET contact_id = NULL WHERE id = ?`, groupID); err != nil {
		t.Fatal(err)
	}
	// Conversation with no real messages — not eligible.
	if _, err := st.UpsertConversation(ctx, source.Signal, "Empty"); err != nil {
		t.Fatal(err)
	}

	convs, err := st.FactConversations(ctx, []string{"Secret"})
	if err != nil {
		t.Fatal(err)
	}
	ids := map[int64]bool{}
	for _, c := range convs {
		ids[c.ID] = true
	}
	if !ids[keep] {
		t.Errorf("eligible set %v missing Harper (%d)", ids, keep)
	}
	if ids[secret] {
		t.Errorf("excluded conversation Secret (%d) leaked into eligible set", secret)
	}
	if len(convs) != 1 {
		t.Errorf("got %d eligible conversations, want 1 (only Harper)", len(convs))
	}
}

func TestFactStateCursorRoundTrip(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	conv := seedConversation(t, st, source.Signal, "Harper")
	hash, _, id, tsUnix := firstMessage(t, st, conv)

	if _, _, ok, err := st.GetFactState(ctx, conv); err != nil || ok {
		t.Fatalf("fresh GetFactState ok=%v err=%v, want false,nil", ok, err)
	}
	if err := st.SetFactState(ctx, conv, hash, "model-a", 3); err != nil {
		t.Fatal(err)
	}
	gotHash, gotModel, ok, err := st.GetFactState(ctx, conv)
	if err != nil || !ok || gotHash != hash || gotModel != "model-a" {
		t.Fatalf("GetFactState = (%q,%q,%v,%v), want (%q,model-a,true,nil)", gotHash, gotModel, ok, err, hash)
	}
	// Cursor resolves the stored hash back to its keyset position.
	ts, gotID, found, err := st.ResolveCursor(ctx, conv, hash)
	if err != nil || !found || ts != tsUnix || gotID != id {
		t.Fatalf("ResolveCursor = (%d,%d,%v,%v), want (%d,%d,true,nil)", ts, gotID, found, err, tsUnix, id)
	}
	// A vanished hash resolves to not-found (caller restarts from the top).
	if _, _, found, err := st.ResolveCursor(ctx, conv, "deadbeef"); err != nil || found {
		t.Fatalf("ResolveCursor(missing) found=%v err=%v, want false,nil", found, err)
	}

	// facts_added accumulates across calls.
	if err := st.SetFactState(ctx, conv, hash, "model-a", 2); err != nil {
		t.Fatal(err)
	}
	var total int
	if err := st.DB().QueryRow(`SELECT facts_added FROM fact_state WHERE conversation_id = ?`, conv).Scan(&total); err != nil {
		t.Fatal(err)
	}
	if total != 5 {
		t.Errorf("facts_added = %d, want 5 (3+2)", total)
	}
}

func TestContactFactsAcrossMergedConversations(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	sig := seedConversation(t, st, source.Signal, "Alex")
	im := seedConversation(t, st, source.IMessage, "+15551234567")

	// Merge the iMessage conversation onto the Signal contact.
	keepContact := contactID(t, st, sig)
	loseContact := contactID(t, st, im)
	if _, err := st.DB().Exec(`UPDATE conversations SET contact_id = ? WHERE id = ?`, keepContact, im); err != nil {
		t.Fatal(err)
	}

	hash, ts, _, tsUnix := firstMessage(t, st, sig)
	if _, err := st.PutFact(ctx, FactInput{
		ContactID: keepContact, Fact: "Lives in Denver", Category: "location",
		Source: source.Signal, SourceMessageHash: hash, SourceTS: ts, SourceTSUnix: tsUnix, Model: "m",
	}); err != nil {
		t.Fatal(err)
	}
	_ = loseContact

	// The fact is visible from BOTH conversations because they share a contact.
	for _, conv := range []int64{sig, im} {
		facts, err := st.ContactFactsByConversation(ctx, conv)
		if err != nil {
			t.Fatal(err)
		}
		if len(facts) != 1 || facts[0].Fact != "Lives in Denver" {
			t.Errorf("conversation %d facts = %+v, want [Lives in Denver]", conv, facts)
		}
	}
}

func TestResetFacts(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	conv := seedConversation(t, st, source.Signal, "Harper")
	cid := contactID(t, st, conv)
	hash, ts, _, tsUnix := firstMessage(t, st, conv)

	if _, err := st.PutFact(ctx, FactInput{
		ContactID: cid, Fact: "Plays cello", Category: "personal",
		Source: source.Signal, SourceMessageHash: hash, SourceTS: ts, SourceTSUnix: tsUnix, Model: "m",
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.SetFactState(ctx, conv, hash, "m", 1); err != nil {
		t.Fatal(err)
	}
	if err := st.ResetFacts(ctx); err != nil {
		t.Fatal(err)
	}
	if n, err := st.CountFacts(ctx); err != nil || n != 0 {
		t.Fatalf("CountFacts after reset = %d (err %v), want 0", n, err)
	}
	if _, _, ok, err := st.GetFactState(ctx, conv); err != nil || ok {
		t.Fatalf("GetFactState after reset ok=%v err=%v, want false", ok, err)
	}
}
