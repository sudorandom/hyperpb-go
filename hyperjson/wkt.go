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
	"strings"
	"time"

	"google.golang.org/protobuf/reflect/protoreflect"
)

// Well-known type full names with custom protojson representations.
const (
	wktAny         = "google.protobuf.Any"
	wktTimestamp   = "google.protobuf.Timestamp"
	wktDuration    = "google.protobuf.Duration"
	wktStruct      = "google.protobuf.Struct"
	wktValue       = "google.protobuf.Value"
	wktListValue   = "google.protobuf.ListValue"
	wktFieldMask   = "google.protobuf.FieldMask"
	wktEmpty       = "google.protobuf.Empty"
	wktBoolValue   = "google.protobuf.BoolValue"
	wktInt32Value  = "google.protobuf.Int32Value"
	wktInt64Value  = "google.protobuf.Int64Value"
	wktUInt32Value = "google.protobuf.UInt32Value"
	wktUInt64Value = "google.protobuf.UInt64Value"
	wktFloatValue  = "google.protobuf.FloatValue"
	wktDoubleValue = "google.protobuf.DoubleValue"
	wktStringValue = "google.protobuf.StringValue"
	wktBytesValue  = "google.protobuf.BytesValue"
	wktNullValue   = "google.protobuf.NullValue"
)

// Well-known-type codes.
const (
	wkNone = iota
	wkTimestamp
	wkDuration
	wkEmpty
	wkStruct
	wkValue
	wkListValue
	wkFieldMask
	wkAny
	wkWrapper
)

// wktCode maps a full message name to its custom-shape code, or wkNone if it
// has a standard message layout.
func wktCode(name protoreflect.FullName) uint8 {
	if !strings.HasPrefix(string(name), "google.protobuf.") {
		return wkNone
	}
	switch string(name) {
	case wktTimestamp:
		return wkTimestamp
	case wktDuration:
		return wkDuration
	case wktEmpty:
		return wkEmpty
	case wktStruct:
		return wkStruct
	case wktValue:
		return wkValue
	case wktListValue:
		return wkListValue
	case wktFieldMask:
		return wkFieldMask
	case wktAny:
		return wkAny
	case wktBoolValue, wktInt32Value, wktInt64Value, wktUInt32Value,
		wktUInt64Value, wktFloatValue, wktDoubleValue, wktStringValue,
		wktBytesValue:
		return wkWrapper
	default:
		return wkNone
	}
}

// isCustomWKT reports whether the message type has a custom protojson shape.
func isCustomWKT(name protoreflect.FullName) bool {
	return wktCode(name) != wkNone
}

// Timestamp bounds: 0001-01-01T00:00:00Z .. 9999-12-31T23:59:59.999999999Z.
const (
	minTimestampSeconds = -62135596800
	maxTimestampSeconds = 253402300799
	maxDurationSeconds  = 315576000000
)

// appendTimestamp appends an RFC 3339 timestamp with Z offset and 0, 3, 6, or
// 9 fractional digits, per the protojson spec.
func appendTimestamp(buf []byte, seconds int64, nanos int32) ([]byte, error) {
	if seconds < minTimestampSeconds || seconds > maxTimestampSeconds || nanos < 0 || nanos > 999999999 {
		return nil, fmt.Errorf("invalid google.protobuf.Timestamp: seconds=%d nanos=%d out of range", seconds, nanos)
	}
	t := time.Unix(seconds, 0).UTC()
	buf = append(buf, '"')
	buf = t.AppendFormat(buf, "2006-01-02T15:04:05")
	buf = appendFraction(buf, nanos)
	buf = append(buf, 'Z', '"')
	return buf, nil
}

// appendFraction appends ".ddd", ".dddddd", or ".ddddddddd" (or nothing for
// zero nanos).
func appendFraction(buf []byte, nanos int32) []byte {
	if nanos == 0 {
		return buf
	}
	buf = append(buf, '.')
	switch {
	case nanos%1e6 == 0:
		buf = appendZeroPadded(buf, uint64(nanos/1e6), 3)
	case nanos%1e3 == 0:
		buf = appendZeroPadded(buf, uint64(nanos/1e3), 6)
	default:
		buf = appendZeroPadded(buf, uint64(nanos), 9)
	}
	return buf
}

func appendZeroPadded(buf []byte, v uint64, width int) []byte {
	var tmp [10]byte
	digits := strconv.AppendUint(tmp[:0], v, 10)
	if pad := width - len(digits); pad > 0 {
		buf = append(buf, "000000000"[:pad]...)
	}
	return append(buf, digits...)
}

// parseTimestamp parses an RFC 3339 timestamp into (seconds, nanos).
func parseTimestamp(s string) (int64, int32, error) {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return 0, 0, fmt.Errorf("invalid google.protobuf.Timestamp value %q", s)
	}
	// protojson limits fractional seconds to 9 digits and offsets to what
	// RFC 3339 allows; time.Parse enforces the general shape.
	seconds := t.Unix()
	if seconds < minTimestampSeconds || seconds > maxTimestampSeconds {
		return 0, 0, fmt.Errorf("google.protobuf.Timestamp value %q out of range", s)
	}
	return seconds, int32(t.Nanosecond()), nil
}

// appendDuration appends a protojson duration: decimal seconds with 0, 3, 6,
// or 9 fractional digits followed by "s". Integer-only arithmetic to avoid
// float precision loss.
func appendDuration(buf []byte, seconds int64, nanos int32) ([]byte, error) {
	if seconds < -maxDurationSeconds || seconds > maxDurationSeconds ||
		nanos <= -1e9 || nanos >= 1e9 ||
		(seconds > 0 && nanos < 0) || (seconds < 0 && nanos > 0) {
		return nil, fmt.Errorf("invalid google.protobuf.Duration: seconds=%d nanos=%d", seconds, nanos)
	}
	buf = append(buf, '"')
	if seconds < 0 || nanos < 0 {
		buf = append(buf, '-')
		seconds = -seconds
		nanos = -nanos
	}
	buf = strconv.AppendInt(buf, seconds, 10)
	buf = appendFraction(buf, nanos)
	buf = append(buf, 's', '"')
	return buf, nil
}

// parseDuration parses a protojson duration string like "-1.5s".
func parseDuration(s string) (int64, int32, error) {
	orig := s
	fail := func() (int64, int32, error) {
		return 0, 0, fmt.Errorf("invalid google.protobuf.Duration value %q", orig)
	}
	if !strings.HasSuffix(s, "s") {
		return fail()
	}
	s = s[:len(s)-1]
	neg := false
	if strings.HasPrefix(s, "-") {
		neg = true
		s = s[1:]
	} else if strings.HasPrefix(s, "+") {
		s = s[1:]
	}
	intPart, fracPart, hasFrac := strings.Cut(s, ".")
	if intPart == "" {
		return fail()
	}
	// The sign was already consumed; ParseUint rejects a smuggled second one.
	useconds, err := strconv.ParseUint(intPart, 10, 64)
	if err != nil || useconds > maxDurationSeconds {
		return fail()
	}
	seconds := int64(useconds)
	var nanos int64
	if hasFrac {
		if fracPart == "" || len(fracPart) > 9 {
			return fail()
		}
		unanos, err := strconv.ParseUint(fracPart, 10, 64)
		if err != nil {
			return fail()
		}
		nanos = int64(unanos)
		for range 9 - len(fracPart) {
			nanos *= 10
		}
	}
	if neg {
		seconds = -seconds
		nanos = -nanos
	}
	return seconds, int32(nanos), nil
}

// jsonCamelCase converts a snake_case field-mask path to camelCase, per
// protobuf JSON mapping rules.
func jsonCamelCase(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	up := false
	for i := range len(s) {
		c := s[i]
		if c == '_' {
			up = true
			continue
		}
		if up && c >= 'a' && c <= 'z' {
			c -= 'a' - 'A'
		}
		up = false
		b.WriteByte(c)
	}
	return b.String()
}

// fieldMaskPathToSnake normalizes one comma-separated FieldMask path the way
// protojson does: surrounding whitespace is trimmed, then the path must be a
// non-empty lowerCamelCase dotted identifier that survives the
// camelCase/snake_case round trip.
func fieldMaskPathToSnake(path string) (string, bool) {
	p := strings.TrimSpace(path)
	if p == "" || strings.Contains(p, "_") {
		return "", false
	}
	for i := range len(p) {
		c := p[i]
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '.') {
			return "", false
		}
	}
	snake := jsonSnakeCase(p)
	if jsonCamelCase(snake) != p {
		return "", false
	}
	return snake, true
}

// jsonSnakeCase converts a camelCase field-mask path back to snake_case.
func jsonSnakeCase(s string) string {
	var b strings.Builder
	b.Grow(len(s) + 4)
	for i := range len(s) {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			b.WriteByte('_')
			c += 'a' - 'A'
		}
		b.WriteByte(c)
	}
	return b.String()
}
