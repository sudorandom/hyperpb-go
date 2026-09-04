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
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"sync"

	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"

	"buf.build/go/hyperpb"
)

// UnmarshalOptions configures Unmarshal.
type UnmarshalOptions struct {
	// DiscardUnknown causes unknown JSON object members to be skipped instead
	// of returning an error.
	DiscardUnknown bool

	// Resolver is used to resolve google.protobuf.Any type URLs. If nil,
	// protoregistry.GlobalTypes is used.
	Resolver Resolver
}

// Unmarshal parses protojson-encoded data into msg, which must be freshly
// allocated (see hyperpb.Shared.NewMessage) and not yet unmarshaled.
//
// This is a proof of concept implemented as a JSON-to-wire transcoder: the
// JSON document is converted directly to wire format guided by a compiled
// per-type plan, and the result is fed to hyperpb's wire-format parser. The
// message zero-copies into the transcoded buffer, which it retains.
func Unmarshal(data []byte, msg *hyperpb.Message) error {
	return UnmarshalOptions{}.Unmarshal(data, msg)
}

// Unmarshal parses protojson-encoded data into msg.
func (o UnmarshalOptions) Unmarshal(data []byte, msg *hyperpb.Message) error {
	dm := unwrapMessage(msg)
	if dp := dplanFor(dm.Type()); dp.direct {
		return o.directUnmarshal(dp, data, dm, msg.Initialized)
	}
	return o.transcodeUnmarshal(data, msg)
}

// transcodeUnmarshal is the JSON-to-wire fallback used when the direct
// writer does not support the message's shape.
func (o UnmarshalOptions) transcodeUnmarshal(data []byte, msg *hyperpb.Message) error {
	t := transcoderPool.Get().(*transcoder) //nolint:errcheck
	t.d = decoder{data: data}
	t.opts = &o
	t.seen = t.seen[:0]
	defer transcoderPool.Put(t)

	p := uplanFor(msg.Descriptor())

	// The output buffer is deliberately not pooled: hyperpb stores zero-copy
	// references into it for the lifetime of msg. Transcoded wire is almost
	// always smaller than its JSON document, so one right-sized allocation
	// avoids growth copies.
	wire := make([]byte, 0, len(data))
	var err error
	if p.wkt != wkNone {
		wire, err = t.wktContent(p, wire)
	} else {
		wire, err = t.message(p, wire, false)
	}
	if err != nil {
		return err
	}
	if !t.d.atEOF() {
		return t.d.errf("unexpected data after top-level value")
	}
	if err := msg.Unmarshal(wire); err != nil {
		return fmt.Errorf("hyperjson: transcoded wire failed to parse: %w", err)
	}
	if p.required {
		return msg.Initialized()
	}
	return nil
}

type transcoder struct {
	d    decoder
	opts *UnmarshalOptions

	// seen is a stack of duplicate/oneof-detection bitsets, one window per
	// in-progress message. Windows are addressed by base index because the
	// backing array may move as nested messages push their windows.
	seen []uint64
}

var transcoderPool = sync.Pool{
	New: func() any { return &transcoder{seen: make([]uint64, 0, 32)} },
}

// beginLen reserves a one-byte length prefix and returns its position.
func beginLen(out []byte) ([]byte, int) {
	out = append(out, 0)
	return out, len(out)
}

// endLen backpatches the length prefix reserved at start. Lengths of 128
// bytes or more shift the just-written content to make room for a longer
// varint.
func endLen(out []byte, start int) []byte {
	n := len(out) - start
	if n < 0x80 {
		out[start-1] = byte(n)
		return out
	}
	extra := protowire.SizeVarint(uint64(n)) - 1
	var zeros [9]byte
	out = append(out, zeros[:extra]...)
	copy(out[start+extra:], out[start:len(out)-extra])
	v := uint64(n)
	for i := start - 1; ; i++ {
		if v < 0x80 {
			out[i] = byte(v)
			break
		}
		out[i] = byte(v) | 0x80
		v >>= 7
	}
	return out
}

// allowsNull reports whether a JSON null is a real value for this field
// rather than "unset".
func allowsNull(fd protoreflect.FieldDescriptor) bool {
	if fd.IsList() || fd.IsMap() {
		return false
	}
	if md := fd.Message(); md != nil && md.FullName() == wktValue {
		return true
	}
	return fd.Kind() == protoreflect.EnumKind && fd.Enum().FullName() == wktNullValue
}

// message transcodes a JSON object into the wire content of p's type,
// appending to out. If skipType is set, a member named "@type" is skipped
// (used for google.protobuf.Any payloads).
func (t *transcoder) message(p *uplan, out []byte, skipType bool) ([]byte, error) {
	if err := t.d.expect('{'); err != nil {
		return nil, err
	}
	t.d.depth++
	if t.d.depth > maxDepth {
		return nil, t.d.errf("exceeded max recursion depth")
	}
	defer func() { t.d.depth-- }()

	if t.d.tryConsume('}') {
		return out, nil
	}

	// Push a bitset window for duplicate and oneof detection.
	base := len(t.seen)
	for range p.words {
		t.seen = append(t.seen, 0)
	}
	defer func() { t.seen = t.seen[:base] }()
	var extSeen map[protowire.Number]bool

	for {
		key, err := t.d.readString()
		if err != nil {
			return nil, err
		}
		if err := t.d.expect(':'); err != nil {
			return nil, err
		}

		switch uf := p.byName[string(key)]; {
		case uf != nil:
			w, bit := base+int(uf.idx)/64, uint64(1)<<(uf.idx%64)
			if t.seen[w]&bit != 0 {
				return nil, t.d.errf("duplicate field %q", key)
			}
			t.seen[w] |= bit

			// JSON null means "unset" for every field except
			// google.protobuf.Value and NullValue-typed fields. A null oneof
			// member does not count as setting the oneof.
			t.d.skipWhitespace()
			if !uf.allowsNull && t.d.off < len(t.d.data) && t.d.data[t.d.off] == 'n' {
				if err := t.d.consumeLiteral("null"); err != nil {
					return nil, err
				}
				break
			}

			if uf.oneof >= 0 {
				w, bit := base+int(uf.oneof)/64, uint64(1)<<(uint32(uf.oneof)%64)
				if t.seen[w]&bit != 0 {
					return nil, t.d.errf("field %q conflicts with another member of its oneof", key)
				}
				t.seen[w] |= bit
			}

			if out, err = t.field(uf, out); err != nil {
				return nil, err
			}

		case skipType && string(key) == "@type":
			if err := t.d.skipValue(); err != nil {
				return nil, err
			}

		case len(key) > 2 && key[0] == '[' && key[len(key)-1] == ']':
			xt, err := protoregistry.GlobalTypes.FindExtensionByName(protoreflect.FullName(key[1 : len(key)-1]))
			if err != nil || xt.TypeDescriptor().ContainingMessage() != p.md {
				if t.opts.DiscardUnknown {
					if err := t.d.skipValue(); err != nil {
						return nil, err
					}
					break
				}
				return nil, t.d.errf("unknown field %q in message %s", key, p.md.FullName())
			}
			ext := classifyU(xt.TypeDescriptor(), map[protoreflect.MessageDescriptor]*uplan{})
			if extSeen == nil {
				extSeen = make(map[protowire.Number]bool, 2)
			}
			if extSeen[ext.num] {
				return nil, t.d.errf("duplicate field %q", key)
			}
			extSeen[ext.num] = true
			if c, err := t.d.peek(); err == nil && c == 'n' && !ext.allowsNull {
				if err := t.d.consumeLiteral("null"); err != nil {
					return nil, err
				}
				break
			}
			if out, err = t.field(ext, out); err != nil {
				return nil, err
			}

		default:
			if !t.opts.DiscardUnknown {
				return nil, t.d.errf("unknown field %q in message %s", key, p.md.FullName())
			}
			if err := t.d.skipValue(); err != nil {
				return nil, err
			}
		}

		if t.d.tryConsume(',') {
			continue
		}
		return out, t.d.expect('}')
	}
}

// field transcodes one field's JSON value (list, map, or singular).
func (t *transcoder) field(uf *ufield, out []byte) ([]byte, error) {
	switch {
	case uf.isMap:
		return t.mapField(uf, out)
	case uf.isList:
		if err := t.d.expect('['); err != nil {
			return nil, err
		}
		t.d.depth++
		if t.d.depth > maxDepth {
			return nil, t.d.errf("exceeded max recursion depth")
		}
		defer func() { t.d.depth-- }()
		if t.d.tryConsume(']') {
			return out, nil
		}
		for {
			var err error
			if out, err = t.singular(uf, out); err != nil {
				return nil, err
			}
			if t.d.tryConsume(',') {
				continue
			}
			return out, t.d.expect(']')
		}
	default:
		return t.singular(uf, out)
	}
}

// mapField transcodes a JSON object into repeated map-entry records.
func (t *transcoder) mapField(uf *ufield, out []byte) ([]byte, error) {
	if err := t.d.expect('{'); err != nil {
		return nil, err
	}
	t.d.depth++
	if t.d.depth > maxDepth {
		return nil, t.d.errf("exceeded max recursion depth")
	}
	defer func() { t.d.depth-- }()
	if t.d.tryConsume('}') {
		return out, nil
	}
	for {
		key, err := t.d.readString()
		if err != nil {
			return nil, err
		}
		if err := t.d.expect(':'); err != nil {
			return nil, err
		}

		entryStart := len(out)
		out = protowire.AppendVarint(out, uf.tag)
		var start int
		out, start = beginLen(out)
		if out, err = t.mapKey(uf.key, key, out); err != nil {
			return nil, err
		}
		afterKey := len(out)
		if out, err = t.singular(uf.val, out); err != nil {
			return nil, err
		}
		if len(out) == afterKey {
			// The value was dropped (unknown enum name under
			// DiscardUnknown); drop the whole entry.
			out = out[:entryStart]
		} else {
			out = endLen(out, start)
		}

		if t.d.tryConsume(',') {
			continue
		}
		return out, t.d.expect('}')
	}
}

// mapKey transcodes a JSON object key into a map entry's key field.
func (t *transcoder) mapKey(uf *ufield, key []byte, out []byte) ([]byte, error) {
	switch uf.class {
	case ucString:
		out = protowire.AppendVarint(out, uf.tag)
		out = protowire.AppendBytes(out, key)
		return out, nil
	case ucBool:
		var v uint64
		switch string(key) {
		case "true":
			v = 1
		case "false":
		default:
			return nil, t.d.errf("invalid map key %q for bool", key)
		}
		out = protowire.AppendVarint(out, uf.tag)
		out = protowire.AppendVarint(out, v)
		return out, nil
	default:
		return t.intValue(uf, key, out)
	}
}

// numericToken reads either a bare JSON number or a quoted numeric string.
func (t *transcoder) numericToken() ([]byte, error) {
	c, err := t.d.peek()
	if err != nil {
		return nil, err
	}
	if c == '"' {
		return t.d.readString()
	}
	return t.d.readNumberToken()
}

// singular transcodes one JSON value into a tagged wire record.
func (t *transcoder) singular(uf *ufield, out []byte) ([]byte, error) {
	switch uf.class {
	case ucBool:
		c, err := t.d.peek()
		if err != nil {
			return nil, err
		}
		var v uint64
		switch c {
		case 't':
			if err := t.d.consumeLiteral("true"); err != nil {
				return nil, err
			}
			v = 1
		case 'f':
			if err := t.d.consumeLiteral("false"); err != nil {
				return nil, err
			}
		default:
			return nil, t.d.errf("invalid value for bool field %s", uf.fd.Name())
		}
		out = protowire.AppendVarint(out, uf.tag)
		out = protowire.AppendVarint(out, v)
		return out, nil

	case ucInt32, ucSint32, ucSfixed32, ucInt64, ucSint64, ucSfixed64,
		ucUint32, ucFixed32, ucUint64, ucFixed64:
		// Single-pass scan-and-parse for plain integers; quoted, float, and
		// exponent forms fall back to the general tokenizer.
		if u, neg, simple := t.d.scanInt(); simple {
			if out, ok := t.fastIntRecord(uf, u, neg, out); ok {
				return out, nil
			}
			return nil, t.d.errf("field %s: integer out of range", uf.fd.Name())
		}
		tok, err := t.numericToken()
		if err != nil {
			return nil, err
		}
		return t.intValue(uf, tok, out)

	case ucFloat, ucDouble:
		return t.floatValue(uf, out)

	case ucString:
		s, err := t.d.readString()
		if err != nil {
			return nil, err
		}
		out = protowire.AppendVarint(out, uf.tag)
		out = protowire.AppendBytes(out, s)
		return out, nil

	case ucBytes:
		s, err := t.d.readString()
		if err != nil {
			return nil, err
		}
		out = protowire.AppendVarint(out, uf.tag)
		var start int
		out, start = beginLen(out)
		if out, err = appendBase64(out, s); err != nil {
			return nil, t.d.errf("invalid base64 in field %s: %v", uf.fd.Name(), err)
		}
		return endLen(out, start), nil

	case ucEnum:
		return t.enumValue(uf, out)

	case ucMessage:
		out = protowire.AppendVarint(out, uf.tag)
		var start int
		out, start = beginLen(out)
		var err error
		if uf.wkt != wkNone {
			out, err = t.wktContent(uf.sub, out)
		} else {
			out, err = t.message(uf.sub, out, false)
		}
		if err != nil {
			return nil, err
		}
		return endLen(out, start), nil

	default: // ucGroup
		out = protowire.AppendVarint(out, uf.tag)
		out, err := t.message(uf.sub, out, false)
		if err != nil {
			return nil, err
		}
		out = protowire.AppendVarint(out, uint64(protowire.EncodeTag(uf.num, protowire.EndGroupType)))
		return out, nil
	}
}

// fastIntRecord range-checks and appends a scanInt result. Returns ok=false
// when the value is out of range for the field ("-0" for unsigned fields also
// lands here, matching the general path's rejection).
func (t *transcoder) fastIntRecord(uf *ufield, u uint64, neg bool, out []byte) ([]byte, bool) {
	var v int64
	switch uf.class {
	case ucInt32, ucSint32, ucSfixed32:
		if (neg && u > 1<<31) || (!neg && u > 1<<31-1) {
			return out, false
		}
		v = int64(u)
		if neg {
			v = -v
		}
	case ucInt64, ucSint64, ucSfixed64:
		if (neg && u > 1<<63) || (!neg && u > 1<<63-1) {
			return out, false
		}
		v = int64(u)
		if neg {
			v = -v
		}
	case ucUint32, ucFixed32:
		if neg || u > 1<<32-1 {
			return out, false
		}
	default: // ucUint64, ucFixed64
		if neg {
			return out, false
		}
	}

	out = protowire.AppendVarint(out, uf.tag)
	switch uf.class {
	case ucSint32, ucSint64:
		out = protowire.AppendVarint(out, protowire.EncodeZigZag(v))
	case ucSfixed32:
		out = protowire.AppendFixed32(out, uint32(v))
	case ucSfixed64:
		out = protowire.AppendFixed64(out, uint64(v))
	case ucInt32, ucInt64:
		out = protowire.AppendVarint(out, uint64(v))
	case ucFixed32:
		out = protowire.AppendFixed32(out, uint32(u))
	case ucFixed64:
		out = protowire.AppendFixed64(out, u)
	default: // ucUint32, ucUint64
		out = protowire.AppendVarint(out, u)
	}
	return out, true
}

// intValue appends an integer wire record parsed from tok.
func (t *transcoder) intValue(uf *ufield, tok []byte, out []byte) ([]byte, error) {
	switch uf.class {
	case ucInt32, ucSint32, ucSfixed32:
		v, err := parseInt(tok, 32)
		if err != nil {
			return nil, t.d.errf("field %s: %v", uf.fd.Name(), err)
		}
		out = protowire.AppendVarint(out, uf.tag)
		switch uf.class {
		case ucSint32:
			out = protowire.AppendVarint(out, protowire.EncodeZigZag(v))
		case ucSfixed32:
			out = protowire.AppendFixed32(out, uint32(v))
		default:
			out = protowire.AppendVarint(out, uint64(v))
		}
	case ucInt64, ucSint64, ucSfixed64:
		v, err := parseInt(tok, 64)
		if err != nil {
			return nil, t.d.errf("field %s: %v", uf.fd.Name(), err)
		}
		out = protowire.AppendVarint(out, uf.tag)
		switch uf.class {
		case ucSint64:
			out = protowire.AppendVarint(out, protowire.EncodeZigZag(v))
		case ucSfixed64:
			out = protowire.AppendFixed64(out, uint64(v))
		default:
			out = protowire.AppendVarint(out, uint64(v))
		}
	case ucUint32, ucFixed32:
		v, err := parseUint(tok, 32)
		if err != nil {
			return nil, t.d.errf("field %s: %v", uf.fd.Name(), err)
		}
		out = protowire.AppendVarint(out, uf.tag)
		if uf.class == ucFixed32 {
			out = protowire.AppendFixed32(out, uint32(v))
		} else {
			out = protowire.AppendVarint(out, v)
		}
	case ucUint64, ucFixed64:
		v, err := parseUint(tok, 64)
		if err != nil {
			return nil, t.d.errf("field %s: %v", uf.fd.Name(), err)
		}
		out = protowire.AppendVarint(out, uf.tag)
		if uf.class == ucFixed64 {
			out = protowire.AppendFixed64(out, v)
		} else {
			out = protowire.AppendVarint(out, v)
		}
	default:
		return nil, t.d.errf("field %s: not an integer kind", uf.fd.Name())
	}
	return out, nil
}

// floatValue appends a float/double wire record.
func (t *transcoder) floatValue(uf *ufield, out []byte) ([]byte, error) {
	c, err := t.d.peek()
	if err != nil {
		return nil, err
	}
	var f float64
	if c == '"' {
		s, err := t.d.readString()
		if err != nil {
			return nil, err
		}
		switch string(s) {
		case "NaN":
			f = math.NaN()
		case "Infinity":
			f = math.Inf(1)
		case "-Infinity":
			f = math.Inf(-1)
		default:
			f, err = strconv.ParseFloat(unsafeString(s), 64)
			if err != nil {
				return nil, t.d.errf("field %s: invalid number %q", uf.fd.Name(), s)
			}
		}
	} else {
		tok, err := t.d.readNumberToken()
		if err != nil {
			return nil, err
		}
		f, err = strconv.ParseFloat(unsafeString(tok), 64)
		if err != nil {
			return nil, t.d.errf("field %s: invalid number %q", uf.fd.Name(), tok)
		}
	}

	if uf.class == ucFloat {
		if !math.IsInf(f, 0) && !math.IsNaN(f) && math.IsInf(float64(float32(f)), 0) {
			return nil, t.d.errf("field %s: value out of range for float", uf.fd.Name())
		}
		out = protowire.AppendVarint(out, uf.tag)
		out = protowire.AppendFixed32(out, math.Float32bits(float32(f)))
	} else {
		out = protowire.AppendVarint(out, uf.tag)
		out = protowire.AppendFixed64(out, math.Float64bits(f))
	}
	return out, nil
}

// enumValue appends an enum wire record from a name string, a number, or
// null (NullValue only).
func (t *transcoder) enumValue(uf *ufield, out []byte) ([]byte, error) {
	c, err := t.d.peek()
	if err != nil {
		return nil, err
	}
	switch {
	case c == 'n' && uf.nullEnum:
		if err := t.d.consumeLiteral("null"); err != nil {
			return nil, err
		}
		out = protowire.AppendVarint(out, uf.tag)
		out = protowire.AppendVarint(out, 0)
		return out, nil
	case c == '"':
		s, err := t.d.readString()
		if err != nil {
			return nil, err
		}
		n, ok := uf.enums[string(s)]
		if !ok {
			if t.opts.DiscardUnknown {
				// Unknown enum names are dropped entirely under
				// DiscardUnknown: the field stays unset, the list element or
				// map entry is omitted.
				return out, nil
			}
			return nil, t.d.errf("unknown enum value %q for field %s", s, uf.fd.Name())
		}
		out = protowire.AppendVarint(out, uf.tag)
		out = protowire.AppendVarint(out, uint64(uint32(n)))
		return out, nil
	default:
		tok, err := t.d.readNumberToken()
		if err != nil {
			return nil, err
		}
		v, err := parseInt(tok, 32)
		if err != nil {
			return nil, t.d.errf("field %s: %v", uf.fd.Name(), err)
		}
		out = protowire.AppendVarint(out, uf.tag)
		out = protowire.AppendVarint(out, uint64(uint32(v)))
		return out, nil
	}
}

// base64Encoding picks the encoding variant for a base64 token: standard or
// URL-safe, padded or raw.
func base64Encoding(s []byte) *base64.Encoding {
	str := unsafeString(s)
	if strings.ContainsAny(str, "-_") {
		if strings.HasSuffix(str, "=") {
			return base64.URLEncoding
		}
		return base64.RawURLEncoding
	}
	if strings.HasSuffix(str, "=") {
		return base64.StdEncoding
	}
	return base64.RawStdEncoding
}

// appendBase64 decodes standard or URL-safe base64 (with or without padding)
// directly into out, avoiding an intermediate buffer.
func appendBase64(out []byte, s []byte) ([]byte, error) {
	enc := base64Encoding(s)
	need := enc.DecodedLen(len(s))
	out = slices.Grow(out, need)
	n, err := enc.Decode(out[len(out):len(out)+need], s)
	if err != nil {
		return nil, err
	}
	return out[:len(out)+n], nil
}

// wktContent transcodes the custom JSON shape of a well-known type into that
// message's wire content (untagged; the caller adds the field framing).
func (t *transcoder) wktContent(p *uplan, out []byte) ([]byte, error) {
	switch p.wkt {
	case wkTimestamp:
		s, err := t.d.readString()
		if err != nil {
			return nil, err
		}
		seconds, nanos, err := parseTimestamp(unsafeString(s))
		if err != nil {
			return nil, t.d.errf("%v", err)
		}
		out = protowire.AppendTag(out, 1, protowire.VarintType)
		out = protowire.AppendVarint(out, uint64(seconds))
		out = protowire.AppendTag(out, 2, protowire.VarintType)
		out = protowire.AppendVarint(out, uint64(uint32(nanos)))
		return out, nil

	case wkDuration:
		s, err := t.d.readString()
		if err != nil {
			return nil, err
		}
		seconds, nanos, err := parseDuration(unsafeString(s))
		if err != nil {
			return nil, t.d.errf("%v", err)
		}
		out = protowire.AppendTag(out, 1, protowire.VarintType)
		out = protowire.AppendVarint(out, uint64(seconds))
		out = protowire.AppendTag(out, 2, protowire.VarintType)
		out = protowire.AppendVarint(out, uint64(int64(nanos)))
		return out, nil

	case wkEmpty:
		err := t.d.walkObject(func(key []byte) error {
			if t.opts.DiscardUnknown {
				return t.d.skipValue()
			}
			return t.d.errf("unknown field %q in google.protobuf.Empty", key)
		})
		return out, err

	case wkStruct:
		return t.structContent(out)

	case wkValue:
		return t.valueContent(out)

	case wkListValue:
		return t.listValueContent(out)

	case wkFieldMask:
		s, err := t.d.readString()
		if err != nil {
			return nil, err
		}
		if len(s) == 0 {
			return out, nil
		}
		for _, path := range strings.Split(string(s), ",") {
			snake, ok := fieldMaskPathToSnake(path)
			if !ok {
				return nil, t.d.errf("invalid google.protobuf.FieldMask path %q", path)
			}
			out = protowire.AppendTag(out, 1, protowire.BytesType)
			out = protowire.AppendString(out, snake)
		}
		return out, nil

	case wkAny:
		return t.anyContent(out)

	default: // wkWrapper
		return t.singular(p.wrapped, out)
	}
}

// structContent transcodes a JSON object into google.protobuf.Struct content.
func (t *transcoder) structContent(out []byte) ([]byte, error) {
	if err := t.d.expect('{'); err != nil {
		return nil, err
	}
	t.d.depth++
	if t.d.depth > maxDepth {
		return nil, t.d.errf("exceeded max recursion depth")
	}
	defer func() { t.d.depth-- }()
	if t.d.tryConsume('}') {
		return out, nil
	}
	for {
		key, err := t.d.readString()
		if err != nil {
			return nil, err
		}
		if err := t.d.expect(':'); err != nil {
			return nil, err
		}

		out = protowire.AppendTag(out, 1, protowire.BytesType)
		var entryStart int
		out, entryStart = beginLen(out)
		out = protowire.AppendTag(out, 1, protowire.BytesType)
		out = protowire.AppendBytes(out, key)
		out = protowire.AppendTag(out, 2, protowire.BytesType)
		var valStart int
		out, valStart = beginLen(out)
		if out, err = t.valueContent(out); err != nil {
			return nil, err
		}
		out = endLen(out, valStart)
		out = endLen(out, entryStart)

		if t.d.tryConsume(',') {
			continue
		}
		return out, t.d.expect('}')
	}
}

// valueContent transcodes any JSON value into google.protobuf.Value content.
func (t *transcoder) valueContent(out []byte) ([]byte, error) {
	c, err := t.d.peek()
	if err != nil {
		return nil, err
	}
	switch c {
	case 'n':
		if err := t.d.consumeLiteral("null"); err != nil {
			return nil, err
		}
		out = protowire.AppendTag(out, 1, protowire.VarintType)
		out = protowire.AppendVarint(out, 0)
	case 't', 'f':
		lit, v := "true", uint64(1)
		if c == 'f' {
			lit, v = "false", 0
		}
		if err := t.d.consumeLiteral(lit); err != nil {
			return nil, err
		}
		out = protowire.AppendTag(out, 4, protowire.VarintType)
		out = protowire.AppendVarint(out, v)
	case '"':
		s, err := t.d.readString()
		if err != nil {
			return nil, err
		}
		out = protowire.AppendTag(out, 3, protowire.BytesType)
		out = protowire.AppendBytes(out, s)
	case '{':
		out = protowire.AppendTag(out, 5, protowire.BytesType)
		var start int
		out, start = beginLen(out)
		if out, err = t.structContent(out); err != nil {
			return nil, err
		}
		out = endLen(out, start)
	case '[':
		out = protowire.AppendTag(out, 6, protowire.BytesType)
		var start int
		out, start = beginLen(out)
		if out, err = t.listValueContent(out); err != nil {
			return nil, err
		}
		out = endLen(out, start)
	default:
		tok, err := t.d.readNumberToken()
		if err != nil {
			return nil, err
		}
		f, err := strconv.ParseFloat(unsafeString(tok), 64)
		if err != nil {
			return nil, t.d.errf("invalid number %q", tok)
		}
		out = protowire.AppendTag(out, 2, protowire.Fixed64Type)
		out = protowire.AppendFixed64(out, math.Float64bits(f))
	}
	return out, nil
}

// listValueContent transcodes a JSON array into google.protobuf.ListValue
// content.
func (t *transcoder) listValueContent(out []byte) ([]byte, error) {
	if err := t.d.expect('['); err != nil {
		return nil, err
	}
	t.d.depth++
	if t.d.depth > maxDepth {
		return nil, t.d.errf("exceeded max recursion depth")
	}
	defer func() { t.d.depth-- }()
	if t.d.tryConsume(']') {
		return out, nil
	}
	for {
		out = protowire.AppendTag(out, 1, protowire.BytesType)
		var start int
		var err error
		out, start = beginLen(out)
		if out, err = t.valueContent(out); err != nil {
			return nil, err
		}
		out = endLen(out, start)

		if t.d.tryConsume(',') {
			continue
		}
		return out, t.d.expect(']')
	}
}

// anyContent transcodes a JSON object into google.protobuf.Any content. This
// requires resolving the @type member, which may appear anywhere in the
// object, so the object is scanned twice.
func (t *transcoder) anyContent(out []byte) ([]byte, error) {
	// First pass: find @type.
	save := t.d
	var typeURL []byte
	err := t.d.walkObject(func(key []byte) error {
		if string(key) == "@type" {
			if typeURL != nil {
				return t.d.errf("duplicate @type in google.protobuf.Any")
			}
			s, err := t.d.readString()
			if err != nil {
				return err
			}
			// Copy: the first pass's view must survive the rewind.
			typeURL = append([]byte(nil), s...)
			return nil
		}
		return t.d.skipValue()
	})
	if err != nil {
		return nil, err
	}
	if typeURL == nil {
		// protojson permits an empty Any as {}.
		if save.tryConsume('{') && save.tryConsume('}') {
			return out, nil
		}
		return nil, t.d.errf("google.protobuf.Any is missing @type")
	}

	resolver := t.opts.Resolver
	if resolver == nil {
		resolver = protoregistry.GlobalTypes
	}
	mt, err := resolver.FindMessageByURL(string(typeURL))
	if err != nil {
		return nil, t.d.errf("cannot resolve google.protobuf.Any type URL %q: %v", typeURL, err)
	}
	inner := uplanFor(mt.Descriptor())

	out = protowire.AppendTag(out, 1, protowire.BytesType)
	out = protowire.AppendBytes(out, typeURL)

	// Second pass: rewind and transcode the payload into a detached buffer.
	t.d = save
	var payload []byte

	if inner.wkt != wkNone {
		// Shape: {"@type": ..., "value": <custom JSON>}.
		var sawValue bool
		err := t.d.walkObject(func(key []byte) error {
			switch string(key) {
			case "@type":
				return t.d.skipValue()
			case "value":
				if sawValue {
					return t.d.errf("duplicate value in google.protobuf.Any")
				}
				sawValue = true
				var err error
				payload, err = t.wktContent(inner, payload)
				return err
			default:
				return t.d.errf("unknown field %q in google.protobuf.Any", key)
			}
		})
		if err != nil {
			return nil, err
		}
		// google.protobuf.Empty's JSON form is {}, so its "value" member may
		// be omitted entirely; every other WKT payload requires one.
		if !sawValue && inner.wkt != wkEmpty {
			return nil, t.d.errf("google.protobuf.Any of type %q is missing value", typeURL)
		}
	} else {
		if payload, err = t.message(inner, payload, true); err != nil {
			return nil, err
		}
	}

	norm, err := normalizeAnyWire(mt.Descriptor(), payload)
	if err != nil {
		return nil, t.d.errf("google.protobuf.Any payload: %v", err)
	}
	out = protowire.AppendTag(out, 2, protowire.BytesType)
	out = protowire.AppendBytes(out, norm)
	return out, nil
}

// normalizeAnyWire re-marshals a transcoded Any payload canonically, the way
// protojson builds Any values (parse the inner message, then marshal it):
// zero-valued implicit-presence fields are elided and fields are emitted in
// field order, keeping Any byte equality compatible with protojson. This must
// use the field-walking marshaler directly: proto.Marshal's unmutated-root
// fast path would re-emit our transcoded input verbatim, skipping
// normalization.
func normalizeAnyWire(md protoreflect.MessageDescriptor, wire []byte) ([]byte, error) {
	inner := hyperpb.NewMessage(compiledType(md))
	if err := inner.Unmarshal(wire); err != nil {
		return nil, err
	}
	return unwrapMessage(inner).MarshalMessage(nil)
}
