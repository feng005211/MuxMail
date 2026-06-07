package domain

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"sort"
	"strconv"
	"strings"
)

const (
	requestIDPrefix = "req_"
	messageIDPrefix = "msg_"
	idBodyLength    = 26
)

const crockfordBase32Alphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

// NewRequestID creates a request ID with the req_ prefix and a 26-character body.
func NewRequestID() (string, error) {
	return newPrefixedID(requestIDPrefix)
}

// NewMessageID creates a message ID with the msg_ prefix and a 26-character body.
func NewMessageID() (string, error) {
	return newPrefixedID(messageIDPrefix)
}

// NormalizeEmail returns the first-phase normalized email form.
func NormalizeEmail(email string) string {
	return strings.ToLower(email)
}

// ToHash returns sha256(app + ":" + normalizedToEmail) as lowercase hex.
func ToHash(app string, normalizedToEmail string) string {
	return sha256Hex(app + ":" + normalizedToEmail)
}

// UserIDHash returns sha256(app + ":" + userID) as lowercase hex, or empty when userID is empty.
func UserIDHash(app string, userID string) string {
	if userID == "" {
		return ""
	}

	return sha256Hex(app + ":" + userID)
}

// IdempotencyHash returns sha256(app + ":" + scene + ":" + idempotencyKey) as lowercase hex.
func IdempotencyHash(app string, scene string, idempotencyKey string) string {
	return sha256Hex(app + ":" + scene + ":" + idempotencyKey)
}

// APIKeyHash returns sha256(apiKey) as lowercase hex.
func APIKeyHash(apiKey string) string {
	return sha256Hex(apiKey)
}

// ConstantTimeEqualHex reports whether two lowercase hex hashes are equal without data-dependent timing.
func ConstantTimeEqualHex(left string, right string) bool {
	if len(left) != len(right) {
		return false
	}

	return subtle.ConstantTimeCompare([]byte(left), []byte(right)) == 1
}

// RequestFingerprint hashes the canonical send request fields used for idempotency comparison.
func RequestFingerprint(normalizedToEmail string, locale string, vars map[string]any) (string, error) {
	canonical, err := canonicalRequestJSON(normalizedToEmail, locale, vars)
	if err != nil {
		return "", err
	}

	return sha256Hex(canonical), nil
}

func newPrefixedID(prefix string) (string, error) {
	var builder strings.Builder
	builder.Grow(len(prefix) + idBodyLength)
	builder.WriteString(prefix)

	max := big.NewInt(int64(len(crockfordBase32Alphabet)))
	for i := 0; i < idBodyLength; i++ {
		index, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", fmt.Errorf("generate id: %w", err)
		}
		builder.WriteByte(crockfordBase32Alphabet[index.Int64()])
	}

	return builder.String(), nil
}

func sha256Hex(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func canonicalRequestJSON(normalizedToEmail string, locale string, vars map[string]any) (string, error) {
	var builder strings.Builder
	builder.WriteString(`{"locale":`)
	writeJSONString(&builder, locale)
	builder.WriteString(`,"to":`)
	writeJSONString(&builder, normalizedToEmail)
	builder.WriteString(`,"vars":`)

	if err := writeCanonicalVars(&builder, vars); err != nil {
		return "", err
	}

	builder.WriteString("}")
	return builder.String(), nil
}

func writeCanonicalVars(builder *strings.Builder, vars map[string]any) error {
	if vars == nil {
		builder.WriteString("{}")
		return nil
	}

	keys := make([]string, 0, len(vars))
	for key := range vars {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	builder.WriteString("{")
	for index, key := range keys {
		if index > 0 {
			builder.WriteString(",")
		}
		writeJSONString(builder, key)
		builder.WriteString(":")
		if err := writeCanonicalValue(builder, vars[key]); err != nil {
			return fmt.Errorf("canonical var %q: %w", key, err)
		}
	}
	builder.WriteString("}")

	return nil
}

func writeCanonicalValue(builder *strings.Builder, value any) error {
	switch typed := value.(type) {
	case string:
		writeJSONString(builder, typed)
	case bool:
		builder.WriteString(strconv.FormatBool(typed))
	case int:
		builder.WriteString(strconv.FormatInt(int64(typed), 10))
	case int8:
		builder.WriteString(strconv.FormatInt(int64(typed), 10))
	case int16:
		builder.WriteString(strconv.FormatInt(int64(typed), 10))
	case int32:
		builder.WriteString(strconv.FormatInt(int64(typed), 10))
	case int64:
		builder.WriteString(strconv.FormatInt(typed, 10))
	case uint:
		builder.WriteString(strconv.FormatUint(uint64(typed), 10))
	case uint8:
		builder.WriteString(strconv.FormatUint(uint64(typed), 10))
	case uint16:
		builder.WriteString(strconv.FormatUint(uint64(typed), 10))
	case uint32:
		builder.WriteString(strconv.FormatUint(uint64(typed), 10))
	case uint64:
		builder.WriteString(strconv.FormatUint(typed, 10))
	case float32:
		builder.WriteString(strconv.FormatFloat(float64(typed), 'g', -1, 32))
	case float64:
		builder.WriteString(strconv.FormatFloat(typed, 'g', -1, 64))
	case json.Number:
		builder.WriteString(typed.String())
	default:
		return fmt.Errorf("unsupported value type %T", value)
	}

	return nil
}

func writeJSONString(builder *strings.Builder, value string) {
	encoded, _ := json.Marshal(value)
	builder.Write(encoded)
}
