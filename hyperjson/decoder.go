// Copyright 2025 Buf Technologies, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package hyperjson

import (
	"fmt"
	"strconv"
	"unicode/utf16"
	"unicode/utf8"
	"unsafe"
)

// decoder is a cursor over a JSON document. It is a hand-rolled lexer
// specialized for the protojson subset: it never builds a token tree, and
// string reads are zero-copy when the string contains no escapes.
type decoder struct {
	data  []byte
	off   int
	depth int
}

const maxDepth = 100

// jsonError is a parse error with a byte offset into the input.
type jsonError struct {
	off int
	msg string
}

func (e *jsonError) Error() string {
	return fmt.Sprintf("hyperjson: syntax error at offset %d: %s", e.off, e.msg)
}

// Offset returns the approximate offset at which the error occurred, matching
// the convention of hyperpb.Message.Unmarshal errors.
func (e *jsonError) Offset() int { return e.off }

func (d *decoder) errf(format string, args ...any) error {
	return &jsonError{off: d.off, msg: fmt.Sprintf(format, args...)}
}

func (d *decoder) skipWhitespace() {
	for d.off < len(d.data) {
		switch d.data[d.off] {
		case ' ', '\t', '\n', '\r':
			d.off++
		default:
			return
		}
	}
}

// peek returns the next non-whitespace byte without consuming it.
func (d *decoder) peek() (byte, error) {
	d.skipWhitespace()
	if d.off >= len(d.data) {
		return 0, d.errf("unexpected end of input")
	}
	return d.data[d.off], nil
}

// expect consumes the next non-whitespace byte, which must be c. The
// no-whitespace case is fast-pathed; compact documents never scan.
func (d *decoder) expect(c byte) error {
	if d.off < len(d.data) && d.data[d.off] == c {
		d.off++
		return nil
	}
	got, err := d.peek()
	if err != nil {
		return err
	}
	if got != c {
		return d.errf("expected %q, found %q", c, got)
	}
	d.off++
	return nil
}

// tryConsume consumes the next non-whitespace byte if it is c.
func (d *decoder) tryConsume(c byte) bool {
	if d.off < len(d.data) {
		switch b := d.data[d.off]; {
		case b == c:
			d.off++
			return true
		case b != ' ' && b != '\t' && b != '\n' && b != '\r':
			return false
		}
	}
	got, err := d.peek()
	if err != nil || got != c {
		return false
	}
	d.off++
	return true
}

// consumeLiteral consumes the given keyword (true/false/null).
func (d *decoder) consumeLiteral(lit string) error {
	if len(d.data)-d.off < len(lit) || string(d.data[d.off:d.off+len(lit)]) != lit {
		return d.errf("invalid literal")
	}
	d.off += len(lit)
	return nil
}

// strSpecial marks bytes that interrupt a plain ASCII string scan: the
// terminating quote, escapes, control characters, and non-ASCII lead bytes
// (which need UTF-8 validation).
var strSpecial = [256]bool{'"': true, '\\': true}

func init() {
	for c := range 0x20 {
		strSpecial[c] = true
	}
	for c := 0x80; c < 0x100; c++ {
		strSpecial[c] = true
	}
}

// readStringView reads a JSON string.
// If the string contains no escape sequences, escaped is false and start is the
// offset within d.data where the string bytes begin. If escaped is true, the
// returned slice is freshly allocated.
func (d *decoder) readStringView() (s []byte, start int, escaped bool, err error) {
	if err := d.expect('"'); err != nil {
		return nil, 0, false, err
	}
	start = d.off
	data := d.data
	i := d.off
	for i < len(data) {
		if !strSpecial[data[i]] {
			i++
			continue
		}
		switch c := data[i]; {
		case c == '"':
			d.off = i + 1
			return data[start:i], start, false, nil
		case c == '\\':
			d.off = i
			s, err := d.readStringSlow(start)
			return s, 0, true, err
		case c < 0x20:
			d.off = i
			return nil, 0, false, d.errf("invalid control character in string")
		default:
			r, size := utf8.DecodeRune(data[i:])
			if r == utf8.RuneError && size == 1 {
				d.off = i
				return nil, 0, false, d.errf("invalid UTF-8 in string")
			}
			i += size
		}
	}
	d.off = i
	return nil, 0, false, d.errf("unterminated string")
}

// readString reads a JSON string. The returned slice aliases the input
// unless the string contained escapes, in which case it is freshly allocated.
// The caller must not retain the result beyond the input's lifetime unless it
// copies it.
func (d *decoder) readString() ([]byte, error) {
	s, _, _, err := d.readStringView()
	return s, err
}

// readStringSlow handles strings with escape sequences, allocating a buffer.
func (d *decoder) readStringSlow(start int) ([]byte, error) {
	buf := make([]byte, 0, len(d.data[start:])+8)
	buf = append(buf, d.data[start:d.off]...)
	for d.off < len(d.data) {
		c := d.data[d.off]
		switch {
		case c == '"':
			d.off++
			return buf, nil
		case c == '\\':
			d.off++
			if d.off >= len(d.data) {
				return nil, d.errf("unterminated escape")
			}
			e := d.data[d.off]
			d.off++
			switch e {
			case '"', '\\', '/':
				buf = append(buf, e)
			case 'b':
				buf = append(buf, '\b')
			case 'f':
				buf = append(buf, '\f')
			case 'n':
				buf = append(buf, '\n')
			case 'r':
				buf = append(buf, '\r')
			case 't':
				buf = append(buf, '\t')
			case 'u':
				r, err := d.readHexRune()
				if err != nil {
					return nil, err
				}
				if utf16.IsSurrogate(r) {
					if d.off+1 < len(d.data) && d.data[d.off] == '\\' && d.data[d.off+1] == 'u' {
						d.off += 2
						r2, err := d.readHexRune()
						if err != nil {
							return nil, err
						}
						if combined := utf16.DecodeRune(r, r2); combined != utf8.RuneError {
							buf = utf8.AppendRune(buf, combined)
							continue
						}
						return nil, d.errf("invalid surrogate pair")
					}
					return nil, d.errf("unpaired surrogate")
				}
				buf = utf8.AppendRune(buf, r)
			default:
				return nil, d.errf("invalid escape character %q", e)
			}
		case c < 0x20:
			return nil, d.errf("invalid control character in string")
		case c < utf8.RuneSelf:
			buf = append(buf, c)
			d.off++
		default:
			r, size := utf8.DecodeRune(d.data[d.off:])
			if r == utf8.RuneError && size == 1 {
				return nil, d.errf("invalid UTF-8 in string")
			}
			buf = append(buf, d.data[d.off:d.off+size]...)
			d.off += size
		}
	}
	return nil, d.errf("unterminated string")
}

func (d *decoder) readHexRune() (rune, error) {
	if len(d.data)-d.off < 4 {
		return 0, d.errf("truncated \\u escape")
	}
	var r rune
	for range 4 {
		c := d.data[d.off]
		switch {
		case c >= '0' && c <= '9':
			r = r<<4 | rune(c-'0')
		case c >= 'a' && c <= 'f':
			r = r<<4 | rune(c-'a'+10)
		case c >= 'A' && c <= 'F':
			r = r<<4 | rune(c-'A'+10)
		default:
			return 0, d.errf("invalid \\u escape")
		}
		d.off++
	}
	return r, nil
}

// readNumberToken consumes a JSON number token and returns the raw bytes.
func (d *decoder) readNumberToken() ([]byte, error) {
	d.skipWhitespace()
	start := d.off
	if d.off < len(d.data) && d.data[d.off] == '-' {
		d.off++
	}
	// Integer part.
	switch {
	case d.off < len(d.data) && d.data[d.off] == '0':
		d.off++
	case d.off < len(d.data) && d.data[d.off] >= '1' && d.data[d.off] <= '9':
		for d.off < len(d.data) && d.data[d.off] >= '0' && d.data[d.off] <= '9' {
			d.off++
		}
	default:
		return nil, d.errf("invalid number")
	}
	// Fraction.
	if d.off < len(d.data) && d.data[d.off] == '.' {
		d.off++
		if d.off >= len(d.data) || d.data[d.off] < '0' || d.data[d.off] > '9' {
			return nil, d.errf("invalid number")
		}
		for d.off < len(d.data) && d.data[d.off] >= '0' && d.data[d.off] <= '9' {
			d.off++
		}
	}
	// Exponent.
	if d.off < len(d.data) && (d.data[d.off] == 'e' || d.data[d.off] == 'E') {
		d.off++
		if d.off < len(d.data) && (d.data[d.off] == '+' || d.data[d.off] == '-') {
			d.off++
		}
		if d.off >= len(d.data) || d.data[d.off] < '0' || d.data[d.off] > '9' {
			return nil, d.errf("invalid number")
		}
		for d.off < len(d.data) && d.data[d.off] >= '0' && d.data[d.off] <= '9' {
			d.off++
		}
	}
	return d.data[start:d.off], nil
}

// scanInt scans and parses a simple integer in one pass: an optionally
// negated run of up to 19 digits, bare or quoted, with no fraction or
// exponent. Returns simple=false (with the cursor unmoved) for anything else
// so the caller can take the general path.
func (d *decoder) scanInt() (u uint64, neg, simple bool) {
	d.skipWhitespace()
	start := d.off
	i := d.off
	quoted := false
	if i < len(d.data) && d.data[i] == '"' {
		quoted = true
		i++
	}
	if i < len(d.data) && d.data[i] == '-' {
		neg = true
		i++
	}
	digitStart := i
	digits := 0
	for i < len(d.data) {
		c := d.data[i]
		if c < '0' || c > '9' {
			break
		}
		u = u*10 + uint64(c-'0')
		digits++
		i++
	}
	// 19 digits always fits uint64 without overflow; 20 might not. Bare JSON
	// numbers must not have leading zeros; the fallback tokenizer rejects
	// them (quoted integers tolerate them, matching strconv).
	if digits == 0 || digits > 19 || (!quoted && digits > 1 && d.data[digitStart] == '0') {
		return 0, false, false
	}
	if quoted {
		if i >= len(d.data) || d.data[i] != '"' {
			d.off = start
			return 0, false, false
		}
		i++
	} else if i < len(d.data) {
		switch d.data[i] {
		case '.', 'e', 'E':
			return 0, false, false
		}
	}
	d.off = i
	return u, neg, true
}

// skipValue skips over one complete JSON value of any kind, validating its
// syntax.
func (d *decoder) skipValue() error {
	c, err := d.peek()
	if err != nil {
		return err
	}
	switch c {
	case '{':
		return d.walkObject(func(key []byte) error { return d.skipValue() })
	case '[':
		return d.walkArray(func() error { return d.skipValue() })
	case '"':
		_, err := d.readString()
		return err
	case 't':
		return d.consumeLiteral("true")
	case 'f':
		return d.consumeLiteral("false")
	case 'n':
		return d.consumeLiteral("null")
	default:
		_, err := d.readNumberToken()
		return err
	}
}

// walkObject consumes a JSON object, calling f positioned at each member's
// value. f must fully consume the value.
func (d *decoder) walkObject(f func(key []byte) error) error {
	if err := d.expect('{'); err != nil {
		return err
	}
	d.depth++
	if d.depth > maxDepth {
		return d.errf("exceeded max recursion depth")
	}
	defer func() { d.depth-- }()

	if d.tryConsume('}') {
		return nil
	}
	for {
		key, err := d.readString()
		if err != nil {
			return err
		}
		if err := d.expect(':'); err != nil {
			return err
		}
		if err := f(key); err != nil {
			return err
		}
		if d.tryConsume(',') {
			continue
		}
		return d.expect('}')
	}
}

// walkArray consumes a JSON array, calling f positioned at each element.
// f must fully consume the element.
func (d *decoder) walkArray(f func() error) error {
	if err := d.expect('['); err != nil {
		return err
	}
	d.depth++
	if d.depth > maxDepth {
		return d.errf("exceeded max recursion depth")
	}
	defer func() { d.depth-- }()

	if d.tryConsume(']') {
		return nil
	}
	for {
		if err := f(); err != nil {
			return err
		}
		if d.tryConsume(',') {
			continue
		}
		return d.expect(']')
	}
}

// atEOF reports whether only whitespace remains.
func (d *decoder) atEOF() bool {
	d.skipWhitespace()
	return d.off >= len(d.data)
}

// unsafeString views b as a string without copying. The result must not
// outlive b's backing storage.
func unsafeString(b []byte) string {
	if len(b) == 0 {
		return ""
	}
	return unsafe.String(&b[0], len(b))
}

// isIntLiteral reports whether tok is a plain (optionally signed) decimal
// integer literal, with no fraction or exponent.
func isIntLiteral(tok []byte) bool {
	if len(tok) > 0 && (tok[0] == '-' || tok[0] == '+') {
		tok = tok[1:]
	}
	if len(tok) == 0 {
		return false
	}
	for _, c := range tok {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// parseInt parses an integer from a JSON number or string token, accepting
// float forms that are mathematically integral (e.g. 1e3, 4.0), matching
// protojson.
func parseInt(tok []byte, bits int) (int64, error) {
	s := unsafeString(tok)
	if v, err := strconv.ParseInt(s, 10, bits); err == nil {
		return v, nil
	}
	// For a plain integer literal, ParseInt's verdict is final.
	if isIntLiteral(tok) {
		return 0, fmt.Errorf("integer out of range: %q", tok)
	}
	norm, ok := normalizeIntDecimal(tok)
	if !ok {
		return 0, fmt.Errorf("invalid integer: %q", tok)
	}
	v, err := strconv.ParseInt(norm, 10, bits)
	if err != nil {
		return 0, fmt.Errorf("integer out of range: %q", tok)
	}
	return v, nil
}

// parseFloatBytes parses a float64 from raw token bytes.
func parseFloatBytes(tok []byte) (float64, error) {
	return strconv.ParseFloat(unsafeString(tok), 64)
}

// parseUint is parseInt for unsigned integers.
func parseUint(tok []byte, bits int) (uint64, error) {
	s := unsafeString(tok)
	if v, err := strconv.ParseUint(s, 10, bits); err == nil {
		return v, nil
	}
	if isIntLiteral(tok) {
		return 0, fmt.Errorf("unsigned integer out of range: %q", tok)
	}
	norm, ok := normalizeIntDecimal(tok)
	if !ok {
		return 0, fmt.Errorf("invalid unsigned integer: %q", tok)
	}
	v, err := strconv.ParseUint(norm, 10, bits)
	if err != nil {
		return 0, fmt.Errorf("unsigned integer out of range: %q", tok)
	}
	return v, nil
}

// normalizeIntDecimal converts a JSON number token carrying a fraction
// and/or exponent into a plain decimal integer string using exact decimal
// arithmetic, matching protojson's normalizeToIntString. Going through
// float64 instead would round values above 2^53 (protojson parses
// "-92233720368.47758e8" as exactly -9223372036847758000) and reject
// in-range values that round to 2^63. Returns ok=false if the token is not
// a well-formed JSON number or is not mathematically an integer.
func normalizeIntDecimal(tok []byte) (string, bool) {
	rest := tok
	var neg bool
	if len(rest) > 0 && rest[0] == '-' {
		neg = true
		rest = rest[1:]
	}

	// Integer part: "0", or a nonzero digit followed by any digits.
	var intDigits []byte
	switch {
	case len(rest) == 0:
		return "", false
	case rest[0] == '0':
		rest = rest[1:]
	case rest[0] >= '1' && rest[0] <= '9':
		n := 1
		for n < len(rest) && rest[n] >= '0' && rest[n] <= '9' {
			n++
		}
		intDigits, rest = rest[:n], rest[n:]
	default:
		return "", false
	}

	// Fraction: '.' followed by one or more digits. Trailing zeros are
	// insignificant, and dropping them here is what lets forms like "4.0"
	// and "150.0e-1" normalize to integers.
	var fracDigits []byte
	if len(rest) > 0 && rest[0] == '.' {
		rest = rest[1:]
		n := 0
		for n < len(rest) && rest[n] >= '0' && rest[n] <= '9' {
			n++
		}
		if n == 0 {
			return "", false
		}
		fracDigits, rest = rest[:n], rest[n:]
		for len(fracDigits) > 0 && fracDigits[len(fracDigits)-1] == '0' {
			fracDigits = fracDigits[:len(fracDigits)-1]
		}
	}

	// Exponent: e/E, an optional sign, and one or more digits. The magnitude
	// is clamped far above any exponent that could still yield an in-range
	// integer, so arbitrarily long exponents cannot overflow.
	exp := 0
	if len(rest) > 0 && (rest[0] == 'e' || rest[0] == 'E') {
		rest = rest[1:]
		expNeg := false
		if len(rest) > 0 && (rest[0] == '+' || rest[0] == '-') {
			expNeg = rest[0] == '-'
			rest = rest[1:]
		}
		if len(rest) == 0 {
			return "", false
		}
		for _, c := range rest {
			if c < '0' || c > '9' {
				return "", false
			}
			exp = min(exp*10+int(c-'0'), 100000)
		}
		rest = nil
		if expNeg {
			exp = -exp
		}
	}
	if len(rest) != 0 {
		return "", false
	}

	// Zero stays zero under any exponent; protojson also drops the sign.
	if len(intDigits) == 0 && len(fracDigits) == 0 {
		return "0", true
	}

	var digits []byte
	if exp >= 0 {
		// Shift fraction digits into the integer part, padding with zeros.
		if len(fracDigits) > exp {
			return "", false
		}
		// 20 digits covers every uint64; anything longer is out of range.
		if len(intDigits)+exp > 20 {
			return "", false
		}
		digits = append(digits, intDigits...)
		digits = append(digits, fracDigits...)
		for range exp - len(fracDigits) {
			digits = append(digits, '0')
		}
	} else {
		// Shift integer digits out; all shifted-out digits must be zero.
		if len(fracDigits) > 0 {
			return "", false
		}
		pointIndex := len(intDigits) + exp
		if pointIndex < 0 {
			return "", false
		}
		for _, c := range intDigits[pointIndex:] {
			if c != '0' {
				return "", false
			}
		}
		digits = intDigits[:pointIndex]
		if len(digits) == 0 {
			digits = []byte("0")
		}
	}
	if neg {
		return "-" + string(digits), true
	}
	return string(digits), true
}
