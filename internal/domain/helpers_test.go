package domain

import (
	"regexp"
	"testing"
)

func TestNewIDsUseExpectedPrefixAndBody(t *testing.T) {
	requestID, err := NewRequestID()
	if err != nil {
		t.Fatalf("expected request id: %v", err)
	}
	messageID, err := NewMessageID()
	if err != nil {
		t.Fatalf("expected message id: %v", err)
	}

	assertID(t, requestID, `^req_[0-9A-HJKMNP-TV-Z]{26}$`)
	assertID(t, messageID, `^msg_[0-9A-HJKMNP-TV-Z]{26}$`)
}

func TestNormalizeEmailLowercasesOnly(t *testing.T) {
	got := NormalizeEmail("User.Name+Tag@Gmail.COM")
	if got != "user.name+tag@gmail.com" {
		t.Fatalf("expected lowercase-only normalization, got %q", got)
	}
}

func TestHashHelpersMatchDesignFormulas(t *testing.T) {
	if got := ToHash("project_a", "user@example.com"); got != "05101a1d9716897da8b1b54cb3a50280313122c01501117e463c3b4dadccb582" {
		t.Fatalf("unexpected to hash: %s", got)
	}
	if got := UserIDHash("project_a", "user-123"); got != "0c224b413815f9dbbd31e93ec9f6644307985f7c963ce6736c5921c04b188b0c" {
		t.Fatalf("unexpected user id hash: %s", got)
	}
	if got := UserIDHash("project_a", ""); got != "" {
		t.Fatalf("expected empty user id hash, got %q", got)
	}
	if got := IdempotencyHash("project_a", "register_code", "idem-123"); got != "ec9c6470b54905a89200b3e2bd83edf6d2792cc1273bfaf236bae82554b834a1" {
		t.Fatalf("unexpected idempotency hash: %s", got)
	}
	if got := APIKeyHash("mk_test_123456789012345678901234"); got != "7e3775417d39c4d7416779c57daf497f7a733cf198673af57c9ddb3d3e088c9a" {
		t.Fatalf("unexpected api key hash: %s", got)
	}
}

func TestRequestFingerprintIsDeterministicByVarKeyOrder(t *testing.T) {
	left, err := RequestFingerprint("user@example.com", "en-US", map[string]any{
		"code":           "123456",
		"expire_minutes": 10,
		"urgent":         true,
	})
	if err != nil {
		t.Fatalf("expected fingerprint: %v", err)
	}

	right, err := RequestFingerprint("user@example.com", "en-US", map[string]any{
		"urgent":         true,
		"expire_minutes": 10,
		"code":           "123456",
	})
	if err != nil {
		t.Fatalf("expected fingerprint: %v", err)
	}

	if left != right {
		t.Fatalf("expected fingerprint to be key-order stable, got %s and %s", left, right)
	}
	if left != "463042c483ba9a3b0e0e0a31cb902137f8a45ee149a1bc9479f8c9cb5a157f24" {
		t.Fatalf("unexpected fingerprint: %s", left)
	}

	changedLocale, err := RequestFingerprint("user@example.com", "zh-CN", map[string]any{
		"code":           "123456",
		"expire_minutes": 10,
		"urgent":         true,
	})
	if err != nil {
		t.Fatalf("expected changed locale fingerprint: %v", err)
	}
	if changedLocale == left {
		t.Fatal("expected locale change to change fingerprint")
	}
}

func TestRequestFingerprintRejectsUnsupportedVars(t *testing.T) {
	_, err := RequestFingerprint("user@example.com", "en-US", map[string]any{
		"nested": map[string]any{"code": "123456"},
	})
	if err == nil {
		t.Fatal("expected unsupported nested var to fail")
	}
}

func TestConstantTimeEqualHex(t *testing.T) {
	hash := APIKeyHash("mk_test_123456789012345678901234")

	if !ConstantTimeEqualHex(hash, hash) {
		t.Fatal("expected equal hashes to compare true")
	}
	if ConstantTimeEqualHex(hash, APIKeyHash("mk_test_other_key_123456789012345678")) {
		t.Fatal("expected different hashes to compare false")
	}
	if ConstantTimeEqualHex(hash, hash[:len(hash)-1]) {
		t.Fatal("expected different length hashes to compare false")
	}
}

func assertID(t *testing.T, id string, pattern string) {
	t.Helper()

	matched, err := regexp.MatchString(pattern, id)
	if err != nil {
		t.Fatalf("invalid id pattern: %v", err)
	}
	if !matched {
		t.Fatalf("id %q did not match %s", id, pattern)
	}
}
