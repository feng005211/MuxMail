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

// IsSafeEmailHeaderValue reports whether a value can be used in email headers.
func IsSafeEmailHeaderValue(value string) bool {
	for _, char := range value {
		if char < 0x20 || char == 0x7f {
			return false
		}
	}

	return true
}

// IsSafeEmailDisplayName reports whether a display name can be used in provider payloads.
func IsSafeEmailDisplayName(value string) bool {
	return IsSafeEmailHeaderValue(value)
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
		canonical, err := canonicalJSONNumber(typed)
		if err != nil {
			return err
		}
		builder.WriteString(canonical)
	default:
		return fmt.Errorf("unsupported value type %T", value)
	}

	return nil
}

func canonicalJSONNumber(number json.Number) (string, error) {
	source := number.String()
	index := 0
	negative := false
	if index < len(source) && source[index] == '-' {
		negative = true
		index++
	}
	if index >= len(source) {
		return "", fmt.Errorf("invalid JSON number %q", source)
	}

	integerStart := index
	switch {
	case source[index] == '0':
		index++
		if index < len(source) && isDecimalDigit(source[index]) {
			return "", fmt.Errorf("invalid JSON number %q", source)
		}
	case source[index] >= '1' && source[index] <= '9':
		for index < len(source) && isDecimalDigit(source[index]) {
			index++
		}
	default:
		return "", fmt.Errorf("invalid JSON number %q", source)
	}
	integerPart := source[integerStart:index]

	fractionPart := ""
	if index < len(source) && source[index] == '.' {
		index++
		fractionStart := index
		for index < len(source) && isDecimalDigit(source[index]) {
			index++
		}
		if fractionStart == index {
			return "", fmt.Errorf("invalid JSON number %q", source)
		}
		fractionPart = source[fractionStart:index]
	}

	exponent := big.NewInt(-int64(len(fractionPart)))
	if index < len(source) && (source[index] == 'e' || source[index] == 'E') {
		index++
		exponentNegative := false
		if index < len(source) && (source[index] == '+' || source[index] == '-') {
			exponentNegative = source[index] == '-'
			index++
		}
		exponentStart := index
		for index < len(source) && isDecimalDigit(source[index]) {
			index++
		}
		if exponentStart == index {
			return "", fmt.Errorf("invalid JSON number %q", source)
		}
		parsedExponent, ok := new(big.Int).SetString(source[exponentStart:index], 10)
		if !ok {
			return "", fmt.Errorf("invalid JSON number %q", source)
		}
		if exponentNegative {
			parsedExponent.Neg(parsedExponent)
		}
		exponent.Add(exponent, parsedExponent)
	}
	if index != len(source) {
		return "", fmt.Errorf("invalid JSON number %q", source)
	}

	digits := strings.TrimLeft(integerPart+fractionPart, "0")
	if digits == "" {
		return "0", nil
	}
	for exponent.Sign() < 0 && strings.HasSuffix(digits, "0") {
		digits = digits[:len(digits)-1]
		exponent.Add(exponent, big.NewInt(1))
	}

	var builder strings.Builder
	if negative {
		builder.WriteByte('-')
	}

	switch exponent.Sign() {
	case 1:
		_, normalizedExponent := canonicalScientificParts(digits, exponent)
		if _, ok := boundedBigIntToInt(normalizedExponent, defaultMaxTemplateVarBytes); !ok {
			builder.WriteString(canonicalScientificNumber(digits, exponent))
			return builder.String(), nil
		}
		padding, ok := boundedBigIntToInt(exponent, defaultMaxTemplateVarBytes)
		if !ok {
			builder.WriteString(canonicalScientificNumber(digits, exponent))
			return builder.String(), nil
		}
		builder.WriteString(digits)
		builder.WriteString(strings.Repeat("0", padding))
	case 0:
		builder.WriteString(digits)
	case -1:
		placesBig := new(big.Int).Neg(exponent)
		places, ok := boundedBigIntToInt(placesBig, defaultMaxTemplateVarBytes+len(digits))
		if !ok {
			builder.WriteString(canonicalScientificNumber(digits, exponent))
			return builder.String(), nil
		}
		if places < len(digits) {
			split := len(digits) - places
			builder.WriteString(digits[:split])
			builder.WriteByte('.')
			builder.WriteString(digits[split:])
			return builder.String(), nil
		}
		padding := places - len(digits)
		if padding > defaultMaxTemplateVarBytes {
			builder.WriteString(canonicalScientificNumber(digits, exponent))
			return builder.String(), nil
		}
		builder.WriteString("0.")
		builder.WriteString(strings.Repeat("0", padding))
		builder.WriteString(digits)
	}

	return builder.String(), nil
}

func canonicalScientificNumber(digits string, exponent *big.Int) string {
	digits, exponent = canonicalScientificParts(digits, exponent)
	if exponent.Sign() == 0 {
		return digits
	}

	return digits + "e" + exponent.String()
}

func canonicalScientificParts(digits string, exponent *big.Int) (string, *big.Int) {
	exponent = new(big.Int).Set(exponent)
	for strings.HasSuffix(digits, "0") {
		digits = digits[:len(digits)-1]
		exponent.Add(exponent, big.NewInt(1))
	}

	return digits, exponent
}

func boundedBigIntToInt(value *big.Int, max int) (int, bool) {
	if value.Sign() < 0 || value.BitLen() > 63 {
		return 0, false
	}
	int64Value := value.Int64()
	if int64Value > int64(max) {
		return 0, false
	}

	return int(int64Value), true
}

func isDecimalDigit(char byte) bool {
	return char >= '0' && char <= '9'
}

func writeJSONString(builder *strings.Builder, value string) {
	encoded, _ := json.Marshal(value)
	builder.Write(encoded)
}
