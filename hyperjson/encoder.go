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
	"encoding/base64"
	"math"
	"strconv"
	"unicode/utf8"
)

// encoder appends protojson-formatted output into a pooled buffer.
type encoder struct {
	buf    []byte
	indent string
	depth  int
}

// bytes returns an owned copy of the encoded output.
func (e *encoder) bytes() []byte {
	out := make([]byte, len(e.buf))
	copy(out, e.buf)
	return out
}

func (e *encoder) raw(s string)   { e.buf = append(e.buf, s...) }
func (e *encoder) rawByte(c byte) { e.buf = append(e.buf, c) }

func (e *encoder) null() { e.raw("null") }

func (e *encoder) writeIndent() {
	for range e.depth {
		e.raw(e.indent)
	}
}

func (e *encoder) openObject() {
	e.rawByte('{')
	if len(e.indent) > 0 {
		e.depth++
	}
}

func (e *encoder) closeObject(empty bool) {
	if len(e.indent) > 0 {
		e.depth--
		if !empty {
			e.rawByte('\n')
			e.writeIndent()
		}
	}
	e.rawByte('}')
}

func (e *encoder) openArray() {
	e.rawByte('[')
	if len(e.indent) > 0 {
		e.depth++
	}
}

func (e *encoder) closeArray(empty bool) {
	if len(e.indent) > 0 {
		e.depth--
		if !empty {
			e.rawByte('\n')
			e.writeIndent()
		}
	}
	e.rawByte(']')
}

func (e *encoder) memberKey(name string, first bool) bool {
	if len(e.indent) == 0 {
		if !first {
			e.rawByte(',')
		}
		e.str(name)
		e.rawByte(':')
		return false
	}
	if !first {
		e.rawByte(',')
	}
	e.rawByte('\n')
	e.writeIndent()
	e.str(name)
	e.raw(": ")
	return false
}

func (e *encoder) arrayElem(first bool) {
	if len(e.indent) == 0 {
		if !first {
			e.rawByte(',')
		}
		return
	}
	if !first {
		e.rawByte(',')
	}
	e.rawByte('\n')
	e.writeIndent()
}

// comma writes a separator if first is false, and returns false. Usage:
//
//	first = e.comma(first)
func (e *encoder) comma(first bool) bool {
	if !first {
		e.rawByte(',')
	}
	return false
}

const hexDigits = "0123456789abcdef"

// str writes a quoted, escaped JSON string.
func (e *encoder) str(s string) {
	e.buf = append(e.buf, '"')
	last := 0
	for i := 0; i < len(s); {
		c := s[i]
		if c >= 0x20 && c != '"' && c != '\\' && c < utf8.RuneSelf {
			i++
			continue
		}
		if c >= utf8.RuneSelf {
			r, size := utf8.DecodeRuneInString(s[i:])
			if r == utf8.RuneError && size == 1 {
				// Replace invalid UTF-8 with U+FFFD, like protojson.
				e.buf = append(e.buf, s[last:i]...)
				e.buf = utf8.AppendRune(e.buf, utf8.RuneError)
				i += size
				last = i
				continue
			}
			i += size
			continue
		}
		e.buf = append(e.buf, s[last:i]...)
		switch c {
		case '"':
			e.buf = append(e.buf, '\\', '"')
		case '\\':
			e.buf = append(e.buf, '\\', '\\')
		case '\n':
			e.buf = append(e.buf, '\\', 'n')
		case '\r':
			e.buf = append(e.buf, '\\', 'r')
		case '\t':
			e.buf = append(e.buf, '\\', 't')
		case '\b':
			e.buf = append(e.buf, '\\', 'b')
		case '\f':
			e.buf = append(e.buf, '\\', 'f')
		default:
			e.buf = append(e.buf, '\\', 'u', '0', '0', hexDigits[c>>4], hexDigits[c&0xf])
		}
		i++
		last = i
	}
	e.buf = append(e.buf, s[last:]...)
	e.buf = append(e.buf, '"')
}

func (e *encoder) boolean(v bool) {
	if v {
		e.raw("true")
	} else {
		e.raw("false")
	}
}

// int32 and uint32 values are emitted as bare JSON numbers.
func (e *encoder) int32(v int32)   { e.buf = strconv.AppendInt(e.buf, int64(v), 10) }
func (e *encoder) uint32(v uint32) { e.buf = strconv.AppendUint(e.buf, uint64(v), 10) }

// 64-bit integers are emitted as JSON strings, per the protojson spec.
func (e *encoder) int64(v int64) {
	e.rawByte('"')
	e.buf = strconv.AppendInt(e.buf, v, 10)
	e.rawByte('"')
}

func (e *encoder) uint64(v uint64) {
	e.rawByte('"')
	e.buf = strconv.AppendUint(e.buf, v, 10)
	e.rawByte('"')
}

// float writes a float32/float64 value; NaN and infinities become the strings
// protojson requires.
func (e *encoder) float(v float64, bits int) {
	switch {
	case math.IsNaN(v):
		e.raw(`"NaN"`)
	case math.IsInf(v, 1):
		e.raw(`"Infinity"`)
	case math.IsInf(v, -1):
		e.raw(`"-Infinity"`)
	default:
		e.buf = strconv.AppendFloat(e.buf, v, 'g', -1, bits)
	}
}

// base64 writes bytes as a standard-encoding base64 JSON string.
func (e *encoder) base64(v []byte) {
	e.rawByte('"')
	e.buf = base64.StdEncoding.AppendEncode(e.buf, v)
	e.rawByte('"')
}
