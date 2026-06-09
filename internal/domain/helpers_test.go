package domain

import (
	"encoding/json"
	"regexp"
	"strings"
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

func TestIsSafeEmailDisplayName(t *testing.T) {
	for _, value := range []string{"", "MuxMail", "用户中心"} {
		if !IsSafeEmailDisplayName(value) {
			t.Fatalf("expected safe display name %q", value)
		}
	}
	for _, value := range []string{"MuxMail\nBcc: attacker@example.com", "MuxMail\tOps", "MuxMail\x7f"} {
		if IsSafeEmailDisplayName(value) {
			t.Fatalf("expected unsafe display name %q", value)
		}
	}
}

func TestIsSafeEmailHeaderValue(t *testing.T) {
	for _, value := range []string{"Your verification code", "验证码"} {
		if !IsSafeEmailHeaderValue(value) {
			t.Fatalf("expected safe header value %q", value)
		}
	}
	for _, value := range []string{"Subject\r\nBcc: attacker@example.com", "Subject\tOps", "Subject\x7f"} {
		if IsSafeEmailHeaderValue(value) {
			t.Fatalf("expected unsafe header value %q", value)
		}
	}
}

func TestAddrSpecEmailValidation(t *testing.T) {
	if !IsAddrSpecEmail("User@Example.COM") {
		t.Fatal("expected valid addr-spec email")
	}

	normalized, ok := NormalizeAddrSpecEmail(" User@Example.COM ")
	if !ok || normalized != "user@example.com" {
		t.Fatalf("expected normalized addr-spec, got %q ok=%v", normalized, ok)
	}

	domain, ok := AddrSpecEmailDomain("User@Example.COM")
	if !ok || domain != "example.com" {
		t.Fatalf("expected lowercase domain, got %q ok=%v", domain, ok)
	}

	for _, value := range []string{
		"User <user@example.com>",
		"user example@example.com",
		"user\n@example.com",
		"user\v@example.com",
		"usér@example.com",
		`"user"@example.com`,
		".user@example.com",
		"user.@example.com",
		"user..name@example.com",
		"user(name)@example.com",
		"user@example.com (comment)",
		"user@bad..example.com",
		"user@-bad.example.com",
		"user@example-.com",
		"user@bad_domain.example.com",
		"not-an-email",
	} {
		if IsAddrSpecEmail(value) {
			t.Fatalf("expected invalid addr-spec email %q", value)
		}
	}
}

func TestIsTemplateVarName(t *testing.T) {
	for _, value := range []string{"code", "expire_minutes", "user-id", "验证码"} {
		if !IsTemplateVarName(value) {
			t.Fatalf("expected valid template var name %q", value)
		}
	}
	for _, value := range []string{"", "code.value", "bad name", "bad\nname", strings.Repeat("a", 65)} {
		if IsTemplateVarName(value) {
			t.Fatalf("expected invalid template var name %q", value)
		}
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

func TestRequestFingerprintCanonicalizesJSONNumberSemanticValues(t *testing.T) {
	left, err := RequestFingerprint("user@example.com", "en-US", map[string]any{
		"amount": json.Number("1"),
		"scale":  json.Number("1000"),
		"tax":    json.Number("1.2300"),
	})
	if err != nil {
		t.Fatalf("expected fingerprint: %v", err)
	}

	right, err := RequestFingerprint("user@example.com", "en-US", map[string]any{
		"amount": json.Number("1.0"),
		"scale":  json.Number("1e3"),
		"tax":    json.Number("1.23"),
	})
	if err != nil {
		t.Fatalf("expected equivalent fingerprint: %v", err)
	}
	if left != right {
		t.Fatalf("expected semantically equal JSON numbers to share fingerprint, got %s and %s", left, right)
	}

	changed, err := RequestFingerprint("user@example.com", "en-US", map[string]any{
		"amount": json.Number("1.1"),
		"scale":  json.Number("1e3"),
		"tax":    json.Number("1.23"),
	})
	if err != nil {
		t.Fatalf("expected changed fingerprint: %v", err)
	}
	if changed == left {
		t.Fatal("expected different JSON number value to change fingerprint")
	}
}

func TestRequestFingerprintRejectsInvalidJSONNumber(t *testing.T) {
	_, err := RequestFingerprint("user@example.com", "en-US", map[string]any{
		"amount": json.Number("01"),
	})
	if err == nil {
		t.Fatal("expected invalid JSON number to fail")
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

func TestIsValidAPIKeyValue(t *testing.T) {
	valid := "mk_test_123456789012345678901234"
	if !IsValidAPIKeyValue(valid) {
		t.Fatalf("expected valid api key value %q", valid)
	}
	for _, value := range []string{
		"short",
		"mk_test_invalid_key_with_space 123",
		"mk_test_invalid_key_123456_中文",
		"mk_test_invalid_key_with_newline\n123",
	} {
		if IsValidAPIKeyValue(value) {
			t.Fatalf("expected invalid api key value %q", value)
		}
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
