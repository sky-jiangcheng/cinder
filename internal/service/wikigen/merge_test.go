package wikigen

import "testing"

// Pure helpers, pinned because the whole scoring surface depends on them and none
// of them are obvious: the CJK bigram rule and the length floor on edit distance
// are both judgement calls that a later "cleanup" could silently flip.
//
// This test moved here from internal/service/wiki_merge_test.go with the helpers
// it pins (ADR-0060 step three): it needs no database and no service, so it
// belongs where the code does.
func TestMergeSimilarityHelpers(t *testing.T) {
	if !slugShapeMatch("payment-gateway", "payment-gateway-v2") {
		t.Fatal("a prefix relation should match")
	}
	if !slugShapeMatch("cbipay-3-0-6", "cbipay-3-0-7") {
		t.Fatal("a one-rune version drift should match")
	}
	if slugShapeMatch("cat", "cot") {
		t.Fatal("short slugs must not be measured by edit distance")
	}
	if slugShapeMatch("invoice-reconcile", "quota-refresh") {
		t.Fatal("unrelated slugs must not match")
	}

	if !titleTokenMatch("Payment Gateway Retry", "Payment Gateway Timeout") {
		t.Fatal("two shared words out of three should match")
	}
	if titleTokenMatch("支付网关", "支付通道") {
		t.Fatal("one shared bigram must not read as agreement about a topic")
	}
	if titleTokenMatch("Sync Mode", "Async Mode") {
		t.Fatal("a single shared word must not match")
	}

	// CJK bigrams: without them a Chinese title contributes no token at all.
	toks := conceptTokens("支付网关重试", "")
	if len(toks) == 0 {
		t.Fatal("CJK titles must produce tokens")
	}
	var hasPayment bool
	for _, tk := range toks {
		if tk == "支付" {
			hasPayment = true
		}
	}
	if !hasPayment {
		t.Fatalf("expected the 支付 bigram, got %v", toks)
	}

	if got := runeEditDistance("kitten", "sitting"); got != 3 {
		t.Fatalf("kitten/sitting distance = %d, want 3", got)
	}
	if got := runeEditDistance("支付网关", "支付通道"); got != 2 {
		t.Fatalf("CJK edit distance must count characters not bytes, got %d", got)
	}
}
