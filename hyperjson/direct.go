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

// This file implements the direct JSON parser: it writes hyperpb's arena
// message representation straight from JSON tokens, with no wire-format
// intermediate. It mirrors the storage conventions of internal/tdp/thunks;
// see the archetype tables there for the ground truth on layout.
//
// The message's zero-copy source buffer is built as [copy of the JSON
// input][appendix], where the appendix holds anything that cannot alias the
// input: unescaped strings, decoded base64, transcoded google.protobuf.Any
// payloads, and extension wire destined for unknown-field storage. The buffer
// may relocate while growing; zc.Range values are relocation-proof offsets,
// so only the raw source pointers (Shared.Src, repeated Strings/Bytes.Src,
// swiss Table.Scratch) need patching, which happens in one fixup pass at the
// end — including on error paths, so a partially-parsed message never holds
// ranges against a nil source.

import (
	"math"
	"sync"
	"unsafe"

	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/reflect/protoreflect"

	"buf.build/go/hyperpb/internal/arena"
	"buf.build/go/hyperpb/internal/arena/slice"
	"buf.build/go/hyperpb/internal/swiss"
	"buf.build/go/hyperpb/internal/tdp"
	"buf.build/go/hyperpb/internal/tdp/dynamic"
	"buf.build/go/hyperpb/internal/tdp/repeated"
	"buf.build/go/hyperpb/internal/xunsafe"
	"buf.build/go/hyperpb/internal/zc"
)

type writer struct {
	d    decoder
	opts UnmarshalOptions

	shared *dynamic.Shared
	arena  *arena.Arena

	// buf is the message's future zero-copy source: the JSON input followed
	// by the appendix. Arena-backed; relocates by allocating a fresh block.
	buf []byte

	// fixups are addresses of *byte source-pointer slots to patch with the
	// final buffer base.
	fixups []unsafe.Pointer

	seen []uint64
}

var writerPool = sync.Pool{New: func() any { return new(writer) }}

// directUnmarshal parses data into m without a wire intermediate. checkInit
// runs the required-field check on the wrapped message.
func (o UnmarshalOptions) directUnmarshal(p *dplan, data []byte, m *dynamic.Message, checkInit func() error) error {
	w := writerPool.Get().(*writer) //nolint:errcheck
	defer func() {
		w.d = decoder{}
		w.opts = UnmarshalOptions{}
		w.shared = nil
		w.arena = nil
		w.buf = nil
		w.fixups = w.fixups[:0]
		w.seen = w.seen[:0]
		if cap(w.fixups) > 1024 {
			w.fixups = nil
		}
		if cap(w.seen) > 256 {
			w.seen = nil
		}
		writerPool.Put(w)
	}()
	w.d = decoder{data: data}
	w.opts = o
	w.shared = m.Shared
	w.arena = m.Shared.Arena()
	w.fixups = w.fixups[:0]
	w.seen = w.seen[:0]

	// Seed the source buffer with a copy of the input; aliased ranges use
	// identical offsets in the copy.
	need := len(data) + len(data)/4 + 64
	w.buf = unsafe.Slice(w.arena.Alloc(need), need)[:0]
	w.buf = append(w.buf, data...)

	var err error
	if p.wkt != wkNone {
		err = w.wktValue(p, m)
	} else {
		err = w.message(p, m, false)
	}
	if err == nil && !w.d.atEOF() {
		err = w.d.errf("unexpected data after top-level value")
	}

	// Patch source pointers even on error: the message may be partially
	// populated and must never hold ranges against a nil source.
	if len(w.buf) == 0 {
		w.buf = w.buf[:1]
	}
	base := &w.buf[0]
	for _, slot := range w.fixups {
		xunsafe.StoreNoWB((**byte)(slot), base)
	}
	w.shared.Src = base
	w.shared.Len = len(w.buf)
	w.buf = nil // Don't retain arena memory in the pool.

	if err != nil {
		return err
	}
	if p.required {
		return checkInit()
	}
	return nil
}

// append adds bytes to the appendix, returning their range.
func (w *writer) append(s []byte) zc.Range {
	if len(w.buf)+len(s) > cap(w.buf) {
		w.grow(len(s))
	}
	start := len(w.buf)
	w.buf = append(w.buf, s...)
	return zc.NewRaw(start, len(s))
}

// reserve returns a scratch region of the appendix of at least n bytes;
// commit takes how many were actually used and returns their range.
func (w *writer) reserve(n int) []byte {
	if len(w.buf)+n > cap(w.buf) {
		w.grow(n)
	}
	return w.buf[len(w.buf) : len(w.buf)+n]
}

func (w *writer) commit(n int) zc.Range {
	start := len(w.buf)
	w.buf = w.buf[:start+n]
	return zc.NewRaw(start, n)
}

func (w *writer) grow(need int) {
	newCap := max(cap(w.buf)*2, len(w.buf)+need+64)
	nb := unsafe.Slice(w.arena.Alloc(newCap), newCap)[:len(w.buf)]
	copy(nb, w.buf)
	w.buf = nb
}

// rangeFromString converts string bytes into a range: unescaped slices share
// offsets with the buffer's input prefix; escaped copies go to the appendix.
func (w *writer) rangeFromString(s []byte, start int, escaped bool) zc.Range {
	if len(s) == 0 {
		return zc.NewRaw(0, 0)
	}
	if !escaped {
		return zc.NewRaw(start, len(s))
	}
	return w.append(s)
}

// fieldPtr resolves a field's storage, allocating the cold region on demand.
func fieldPtr(m *dynamic.Message, off tdp.Offset) unsafe.Pointer {
	if off.Data >= 0 {
		return unsafe.Add(unsafe.Pointer(m), uintptr(off.Data))
	}
	return unsafe.Add(unsafe.Pointer(m.MutableCold()), uintptr(^off.Data))
}

// alloc allocates a fresh submessage of the given type, mirroring
// dynamic.Shared.New without the lock (the library is already bound).
func (w *writer) alloc(ty *tdp.Type) *dynamic.Message {
	data := w.arena.Alloc(int(ty.Size))
	m := xunsafe.Cast[dynamic.Message](data)
	xunsafe.StoreNoWB(&m.Shared, w.shared)
	m.TypeOffset = uint32(xunsafe.ByteSub(ty, w.shared.Library().Base))
	m.ColdIndex = -1
	return m
}

// message parses a JSON object into m. skipType skips an "@type" member
// (google.protobuf.Any payloads).
func (w *writer) message(p *dplan, m *dynamic.Message, skipType bool) error {
	if err := w.d.expect('{'); err != nil {
		return err
	}
	w.d.depth++
	if w.d.depth > maxDepth {
		return w.d.errf("exceeded max recursion depth")
	}
	defer func() { w.d.depth-- }()

	if w.d.tryConsume('}') {
		return nil
	}

	base := len(w.seen)
	for range p.words {
		w.seen = append(w.seen, 0)
	}
	defer func() { w.seen = w.seen[:base] }()
	var extSeen map[protowire.Number]bool

	var predicted *dfield
	if len(p.byIdx) > 0 {
		predicted = &p.byIdx[0]
	}

	for {
		key, err := w.d.readString()
		if err != nil {
			return err
		}
		if err := w.d.expect(':'); err != nil {
			return err
		}

		// Members usually arrive in schema order; try the predicted field
		// before paying for a map lookup.
		var df *dfield
		if predicted != nil && string(key) == predicted.jsonName {
			df = predicted
		} else {
			df = p.byName[string(key)]
		}
		if df != nil {
			predicted = df.next
		}

		switch {
		case df != nil:
			wd, bit := base+int(df.idx)/64, uint64(1)<<(df.idx%64)
			if w.seen[wd]&bit != 0 {
				return w.d.errf("duplicate field %q", key)
			}
			w.seen[wd] |= bit

			w.d.skipWhitespace()
			if !df.allowsNull && w.d.off < len(w.d.data) && w.d.data[w.d.off] == 'n' {
				if err := w.d.consumeLiteral("null"); err != nil {
					return err
				}
				break
			}

			if df.oneof >= 0 {
				wd, bit := base+int(df.oneof)/64, uint64(1)<<(uint32(df.oneof)%64)
				if w.seen[wd]&bit != 0 {
					return w.d.errf("field %q conflicts with another member of its oneof", key)
				}
				w.seen[wd] |= bit
			}

			if err := w.field(df, m); err != nil {
				return err
			}

		case skipType && string(key) == "@type":
			if err := w.d.skipValue(); err != nil {
				return err
			}

		case len(key) > 2 && key[0] == '[' && key[len(key)-1] == ']':
			var err error
			extSeen, err = w.extension(p, m, key, extSeen)
			if err != nil {
				return err
			}

		default:
			if !w.opts.DiscardUnknown {
				return w.d.errf("unknown field %q in message %s", key, p.md.FullName())
			}
			if err := w.d.skipValue(); err != nil {
				return err
			}
		}

		if w.d.tryConsume(',') {
			continue
		}
		return w.d.expect('}')
	}
}

// field parses one member's value into its storage.
func (w *writer) field(df *dfield, m *dynamic.Message) error {
	switch {
	case df.isMap:
		return w.mapField(df, m)
	case df.isList:
		return w.listField(df, m)
	default:
		return w.singular(df, m)
	}
}

// setPresence applies the field's presence discipline. For dpHasbit bools the
// value lives in the bit after the has-bit; for oneofs the field number goes
// in the which-word.
func setPresence(df *dfield, m *dynamic.Message) {
	switch df.presence {
	case dpHasbit:
		m.SetBit(df.offset.Bit, true)
	case dpOneof:
		xunsafe.ByteStore(m, df.offset.Bit, df.number)
	}
}

func store[T any](m *dynamic.Message, df *dfield, v T) {
	*(*T)(fieldPtr(m, df.offset)) = v
}

// singular parses and stores one non-repeated value.
func (w *writer) singular(df *dfield, m *dynamic.Message) error {
	switch df.class {
	case ucBool:
		v, err := w.readBool()
		if err != nil {
			return err
		}
		switch df.presence {
		case dpOneof:
			setPresence(df, m)
			var b byte
			if v {
				b = 1
			}
			store(m, df, b)
		case dpHasbit:
			m.SetBit(df.offset.Bit, true)
			m.SetBit(df.offset.Bit+1, v)
		default:
			m.SetBit(df.offset.Bit, v)
		}
		return nil

	case ucInt32, ucSint32, ucSfixed32, ucInt64, ucSint64, ucSfixed64,
		ucUint32, ucFixed32, ucUint64, ucFixed64:
		v, err := w.readInt(df)
		if err != nil {
			return err
		}
		setPresence(df, m)
		switch df.class {
		case ucInt32, ucSint32, ucSfixed32:
			store(m, df, int32(v))
		case ucUint32, ucFixed32:
			store(m, df, uint32(v))
		default:
			store(m, df, v)
		}
		return nil

	case ucFloat:
		f, err := w.readFloat(df, 32)
		if err != nil {
			return err
		}
		setPresence(df, m)
		store(m, df, float32(f))
		return nil

	case ucDouble:
		f, err := w.readFloat(df, 64)
		if err != nil {
			return err
		}
		setPresence(df, m)
		store(m, df, f)
		return nil

	case ucString, ucBytes:
		r, err := w.readStringOrBytes(df)
		if err != nil {
			return err
		}
		setPresence(df, m)
		store(m, df, r)
		return nil

	case ucEnum:
		n, set, err := w.readEnum(df)
		if err != nil || !set {
			return err
		}
		setPresence(df, m)
		store(m, df, n)
		return nil

	default: // ucMessage, ucGroup
		// Duplicate detection guarantees this field was never set in this
		// parse, so always allocate fresh (union storage may hold remnants).
		sub := w.alloc(df.subTy)
		setPresence(df, m)
		xunsafe.StoreNoWB((**dynamic.Message)(fieldPtr(m, df.offset)), sub)
		if df.wkt != wkNone {
			return w.wktValue(df.sub, sub)
		}
		return w.message(df.sub, sub, false)
	}
}

// readBool consumes a JSON boolean literal.
func (w *writer) readBool() (bool, error) {
	c, err := w.d.peek()
	if err != nil {
		return false, err
	}
	switch c {
	case 't':
		return true, w.d.consumeLiteral("true")
	case 'f':
		return false, w.d.consumeLiteral("false")
	}
	return false, w.d.errf("invalid value for bool")
}

// readInt consumes an integer (bare, quoted, or integral float form) and
// range-checks it for the field's class. sint values are returned decoded;
// repeated zigzag storage re-encodes at the append site.
func (w *writer) readInt(df *dfield) (int64, error) {
	if u, neg, simple := w.d.scanInt(); simple {
		if v, ok := checkIntRange(df.class, u, neg); ok {
			return v, nil
		}
		return 0, w.d.errf("field %s: integer out of range", df.fd.Name())
	}
	tok, err := w.numericToken()
	if err != nil {
		return 0, err
	}
	switch df.class {
	case ucInt32, ucSint32, ucSfixed32:
		v, err := parseInt(tok, 32)
		if err != nil {
			return 0, w.d.errf("field %s: %v", df.fd.Name(), err)
		}
		return v, nil
	case ucInt64, ucSint64, ucSfixed64:
		v, err := parseInt(tok, 64)
		if err != nil {
			return 0, w.d.errf("field %s: %v", df.fd.Name(), err)
		}
		return v, nil
	case ucUint32, ucFixed32:
		v, err := parseUint(tok, 32)
		if err != nil {
			return 0, w.d.errf("field %s: %v", df.fd.Name(), err)
		}
		return int64(v), nil
	default:
		v, err := parseUint(tok, 64)
		if err != nil {
			return 0, w.d.errf("field %s: %v", df.fd.Name(), err)
		}
		return int64(v), nil
	}
}

func (w *writer) numericToken() ([]byte, error) {
	c, err := w.d.peek()
	if err != nil {
		return nil, err
	}
	if c == '"' {
		return w.d.readString()
	}
	return w.d.readNumberToken()
}

// checkIntRange validates a scanInt result against the class's range.
func checkIntRange(class uint8, u uint64, neg bool) (int64, bool) {
	switch class {
	case ucInt32, ucSint32, ucSfixed32:
		if (neg && u > 1<<31) || (!neg && u > 1<<31-1) {
			return 0, false
		}
	case ucInt64, ucSint64, ucSfixed64:
		if (neg && u > 1<<63) || (!neg && u > 1<<63-1) {
			return 0, false
		}
	case ucUint32, ucFixed32:
		if neg || u > 1<<32-1 {
			return 0, false
		}
	default: // ucUint64, ucFixed64
		if neg {
			return 0, false
		}
	}
	if neg {
		if u == 1<<63 {
			return math.MinInt64, true
		}
		return -int64(u), true
	}
	return int64(u), true
}

// readFloat consumes a float value (number, quoted number, or the special
// NaN/Infinity strings).
func (w *writer) readFloat(df *dfield, bits int) (float64, error) {
	c, err := w.d.peek()
	if err != nil {
		return 0, err
	}
	var f float64
	if c == '"' {
		s, err := w.d.readString()
		if err != nil {
			return 0, err
		}
		switch string(s) {
		case "NaN":
			f = math.NaN()
		case "Infinity":
			f = math.Inf(1)
		case "-Infinity":
			f = math.Inf(-1)
		default:
			f, err = parseFloatBytes(s)
			if err != nil {
				return 0, w.d.errf("field %s: invalid number %q", df.fd.Name(), s)
			}
		}
	} else {
		tok, err := w.d.readNumberToken()
		if err != nil {
			return 0, err
		}
		f, err = parseFloatBytes(tok)
		if err != nil {
			return 0, w.d.errf("field %s: invalid number %q", df.fd.Name(), tok)
		}
	}
	if bits == 32 && !math.IsInf(f, 0) && !math.IsNaN(f) && math.IsInf(float64(float32(f)), 0) {
		return 0, w.d.errf("field %s: value out of range for float", df.fd.Name())
	}
	return f, nil
}

// readStringOrBytes consumes a string value; bytes are base64-decoded into
// the appendix.
func (w *writer) readStringOrBytes(df *dfield) (zc.Range, error) {
	s, start, escaped, err := w.d.readStringView()
	if err != nil {
		return 0, err
	}
	if df.class == ucString {
		return w.rangeFromString(s, start, escaped), nil
	}
	enc := base64Encoding(s)
	dst := w.reserve(enc.DecodedLen(len(s)))
	n, err := enc.Decode(dst, s)
	if err != nil {
		return 0, w.d.errf("invalid base64 in field %s: %v", df.fd.Name(), err)
	}
	return w.commit(n), nil
}

// readEnum consumes an enum value; set=false means the value was an unknown
// name dropped under DiscardUnknown.
func (w *writer) readEnum(df *dfield) (protoreflect.EnumNumber, bool, error) {
	c, err := w.d.peek()
	if err != nil {
		return 0, false, err
	}
	switch {
	case c == 'n' && df.nullEnum:
		return 0, true, w.d.consumeLiteral("null")
	case c == '"':
		s, err := w.d.readString()
		if err != nil {
			return 0, false, err
		}
		n, ok := df.enums[string(s)]
		if !ok {
			if w.opts.DiscardUnknown {
				return 0, false, nil
			}
			return 0, false, w.d.errf("unknown enum value %q for field %s", s, df.fd.Name())
		}
		return n, true, nil
	default:
		tok, err := w.d.readNumberToken()
		if err != nil {
			return 0, false, err
		}
		v, err := parseInt(tok, 32)
		if err != nil {
			return 0, false, w.d.errf("field %s: %v", df.fd.Name(), err)
		}
		return protoreflect.EnumNumber(v), true, nil
	}
}

// appendScalarList appends one element to a Scalars-style repeated field.
func appendScalarList[E any](w *writer, p unsafe.Pointer, v E) {
	up := (*slice.Untyped)(p)
	s := slice.CastUntyped[E](*up).AppendOne(w.arena, v)
	*up = s.Addr().Untyped()
}

// listField parses a JSON array into a repeated field.
func (w *writer) listField(df *dfield, m *dynamic.Message) error {
	if err := w.d.expect('['); err != nil {
		return err
	}
	w.d.depth++
	if w.d.depth > maxDepth {
		return w.d.errf("exceeded max recursion depth")
	}
	defer func() { w.d.depth-- }()
	if w.d.tryConsume(']') {
		return nil
	}

	p := fieldPtr(m, df.offset)
	for {
		if err := w.listElement(df, p); err != nil {
			return err
		}
		if w.d.tryConsume(',') {
			continue
		}
		return w.d.expect(']')
	}
}

func (w *writer) listElement(df *dfield, p unsafe.Pointer) error {
	switch df.class {
	case ucBool:
		v, err := w.readBool()
		if err != nil {
			return err
		}
		var b byte
		if v {
			b = 1
		}
		appendScalarList(w, p, b)

	case ucInt32, ucSfixed32:
		v, err := w.readInt(df)
		if err != nil {
			return err
		}
		appendScalarList(w, p, int32(v))
	case ucSint32:
		v, err := w.readInt(df)
		if err != nil {
			return err
		}
		// Repeated zigzag storage keeps elements encoded.
		appendScalarList(w, p, int32(protowire.EncodeZigZag(v)))
	case ucUint32, ucFixed32:
		v, err := w.readInt(df)
		if err != nil {
			return err
		}
		appendScalarList(w, p, uint32(v))
	case ucInt64, ucSfixed64:
		v, err := w.readInt(df)
		if err != nil {
			return err
		}
		appendScalarList(w, p, v)
	case ucSint64:
		v, err := w.readInt(df)
		if err != nil {
			return err
		}
		appendScalarList(w, p, int64(protowire.EncodeZigZag(v)))
	case ucUint64, ucFixed64:
		v, err := w.readInt(df)
		if err != nil {
			return err
		}
		appendScalarList(w, p, uint64(v))

	case ucFloat:
		f, err := w.readFloat(df, 32)
		if err != nil {
			return err
		}
		appendScalarList(w, p, float32(f))
	case ucDouble:
		f, err := w.readFloat(df, 64)
		if err != nil {
			return err
		}
		appendScalarList(w, p, f)

	case ucString, ucBytes:
		r, err := w.readStringOrBytes(df)
		if err != nil {
			return err
		}
		// repeated.Strings and repeated.Bytes share the {Src, Raw} layout.
		strs := (*repeated.Strings)(p)
		if strs.Raw.Len() == 0 {
			w.fixups = append(w.fixups, unsafe.Pointer(&strs.Src))
		}
		strs.Raw = strs.Raw.AppendOne(w.arena, r)

	case ucEnum:
		n, set, err := w.readEnum(df)
		if err != nil {
			return err
		}
		if set {
			appendScalarList(w, p, n)
		}

	default: // ucMessage, ucGroup
		sub := w.alloc(df.subTy)
		if df.wkt != wkNone {
			if err := w.wktValue(df.sub, sub); err != nil {
				return err
			}
		} else if err := w.message(df.sub, sub, false); err != nil {
			return err
		}
		// Always outlined mode (stride 0): an arena slice of pointers.
		mm := (*repeated.Messages[dynamic.Message])(p)
		s := slice.CastUntyped[*dynamic.Message](mm.Raw).AppendOne(w.arena, sub)
		mm.Raw = s.Addr().Untyped()
	}
	return nil
}

// mapInsert inserts into (or creates/grows) a swiss table field, mirroring
// the map parse thunks.
func mapInsert[K swiss.Key, V any](w *writer, slot unsafe.Pointer, k K, extract func(K) []byte) *V {
	mp := (**swiss.Table[K, V])(slot)
	m := *mp
	if m == nil {
		size, _ := swiss.Layout[K, V](1)
		m = xunsafe.Cast[swiss.Table[K, V]](w.arena.Alloc(size))
		xunsafe.StoreNoWB(mp, m)
		m.Init(1, nil, extract)
		w.fixups = append(w.fixups, unsafe.Pointer(&m.Scratch))
	}
	vp := m.Insert(k, extract)
	if vp == nil {
		size, _ := swiss.Layout[K, V](m.Len() + 1)
		m2 := xunsafe.Cast[swiss.Table[K, V]](w.arena.Alloc(size))
		xunsafe.StoreNoWB(mp, m2)
		m2.Init(m.Len()+1, m, extract)
		w.fixups = append(w.fixups, unsafe.Pointer(&m2.Scratch))
		vp = m2.Insert(k, extract)
	}
	return vp
}

// mapValue is a parsed map value ready for insertion.
type mapValue struct {
	bits uint64 // scalar bits / zc.Range
	msg  *dynamic.Message
	skip bool // dropped unknown enum name
}

// readMapValue parses a map value by class.
func (w *writer) readMapValue(df *dfield) (mapValue, error) {
	val := df.val
	switch val.class {
	case ucBool:
		v, err := w.readBool()
		if err != nil {
			return mapValue{}, err
		}
		var b uint64
		if v {
			b = 1
		}
		return mapValue{bits: b}, nil
	case ucInt32, ucSint32, ucSfixed32, ucInt64, ucSint64, ucSfixed64,
		ucUint32, ucFixed32, ucUint64, ucFixed64:
		v, err := w.readInt(val)
		if err != nil {
			return mapValue{}, err
		}
		return mapValue{bits: uint64(v)}, nil
	case ucFloat:
		f, err := w.readFloat(val, 32)
		if err != nil {
			return mapValue{}, err
		}
		return mapValue{bits: uint64(math.Float32bits(float32(f)))}, nil
	case ucDouble:
		f, err := w.readFloat(val, 64)
		if err != nil {
			return mapValue{}, err
		}
		return mapValue{bits: math.Float64bits(f)}, nil
	case ucString, ucBytes:
		r, err := w.readStringOrBytes(val)
		if err != nil {
			return mapValue{}, err
		}
		return mapValue{bits: uint64(r)}, nil
	case ucEnum:
		n, set, err := w.readEnum(val)
		if err != nil {
			return mapValue{}, err
		}
		return mapValue{bits: uint64(uint32(n)), skip: !set}, nil
	default: // ucMessage
		sub := w.alloc(val.subTy)
		var err error
		if val.wkt != wkNone {
			err = w.wktValue(val.sub, sub)
		} else {
			err = w.message(val.sub, sub, false)
		}
		if err != nil {
			return mapValue{}, err
		}
		return mapValue{msg: sub}, nil
	}
}

// insertMapEntry inserts a parsed value under the typed key.
func insertMapEntry[K swiss.Key](w *writer, df *dfield, slot unsafe.Pointer, k K, extract func(K) []byte, v mapValue) {
	switch df.val.class {
	case ucBool:
		*mapInsert[K, bool](w, slot, k, extract) = v.bits != 0
	case ucInt32, ucSint32, ucSfixed32:
		*mapInsert[K, int32](w, slot, k, extract) = int32(v.bits)
	case ucUint32, ucFixed32:
		*mapInsert[K, uint32](w, slot, k, extract) = uint32(v.bits)
	case ucInt64, ucSint64, ucSfixed64:
		*mapInsert[K, int64](w, slot, k, extract) = int64(v.bits)
	case ucUint64, ucFixed64:
		*mapInsert[K, uint64](w, slot, k, extract) = v.bits
	case ucFloat:
		*mapInsert[K, float32](w, slot, k, extract) = math.Float32frombits(uint32(v.bits))
	case ucDouble:
		*mapInsert[K, float64](w, slot, k, extract) = math.Float64frombits(v.bits)
	case ucEnum:
		*mapInsert[K, protoreflect.EnumNumber](w, slot, k, extract) = protoreflect.EnumNumber(v.bits)
	case ucString, ucBytes:
		*mapInsert[K, zc.Range](w, slot, k, extract) = zc.Range(v.bits)
	default: // ucMessage
		xunsafe.StoreNoWB(mapInsert[K, *dynamic.Message](w, slot, k, extract), v.msg)
	}
}

// mapField parses a JSON object into a map field.
func (w *writer) mapField(df *dfield, m *dynamic.Message) error {
	if err := w.d.expect('{'); err != nil {
		return err
	}
	w.d.depth++
	if w.d.depth > maxDepth {
		return w.d.errf("exceeded max recursion depth")
	}
	defer func() { w.d.depth-- }()
	if w.d.tryConsume('}') {
		return nil
	}

	slot := fieldPtr(m, df.offset)
	for {
		key, start, escaped, err := w.d.readStringView()
		if err != nil {
			return err
		}
		if err := w.d.expect(':'); err != nil {
			return err
		}
		v, err := w.readMapValue(df)
		if err != nil {
			return err
		}
		if !v.skip {
			if err := w.insertByKey(df, slot, key, start, escaped, v); err != nil {
				return err
			}
		}
		if w.d.tryConsume(',') {
			continue
		}
		return w.d.expect('}')
	}
}

// insertByKey parses the key text by key class and dispatches insertion.
func (w *writer) insertByKey(df *dfield, slot unsafe.Pointer, key []byte, start int, escaped bool, v mapValue) error {
	switch df.key.class {
	case ucString:
		r := w.rangeFromString(key, start, escaped)
		ef := zc.ExtractFrom{Src: &w.buf[0]}
		extract := func(k zc.Range) []byte { return ef.Bytes(uint64(k)) }
		insertMapEntry(w, df, slot, r, extract, v)
		return nil
	case ucBool:
		var k byte
		switch string(key) {
		case "true":
			k = 1
		case "false":
		default:
			return w.d.errf("invalid map key %q for bool", key)
		}
		insertMapEntry(w, df, slot, k, nil, v)
		return nil
	case ucInt32, ucSint32, ucSfixed32:
		n, err := parseInt(key, 32)
		if err != nil {
			return w.d.errf("invalid map key %q: %v", key, err)
		}
		insertMapEntry(w, df, slot, int32(n), nil, v)
		return nil
	case ucInt64, ucSint64, ucSfixed64:
		n, err := parseInt(key, 64)
		if err != nil {
			return w.d.errf("invalid map key %q: %v", key, err)
		}
		insertMapEntry(w, df, slot, n, nil, v)
		return nil
	case ucUint32, ucFixed32:
		n, err := parseUint(key, 32)
		if err != nil {
			return w.d.errf("invalid map key %q: %v", key, err)
		}
		insertMapEntry(w, df, slot, uint32(n), nil, v)
		return nil
	default:
		n, err := parseUint(key, 64)
		if err != nil {
			return w.d.errf("invalid map key %q: %v", key, err)
		}
		insertMapEntry(w, df, slot, n, nil, v)
		return nil
	}
}

// extension handles a "[full.name]" member: compiled extensions write their
// field directly; unresolvable or uncompiled extensions are transcoded to
// wire and preserved as unknown fields, matching the transcode path.
func (w *writer) extension(p *dplan, m *dynamic.Message, key []byte, extSeen map[protowire.Number]bool) (map[protowire.Number]bool, error) {
	xt, err := findExtension(w.opts.Resolver, protoreflect.FullName(key[1:len(key)-1]))
	if err != nil || xt.TypeDescriptor().ContainingMessage() != p.md {
		if w.opts.DiscardUnknown {
			return extSeen, w.d.skipValue()
		}
		return extSeen, w.d.errf("unknown field %q in message %s", key, p.md.FullName())
	}
	xd := xt.TypeDescriptor()

	if extSeen == nil {
		extSeen = make(map[protowire.Number]bool, 2)
	}
	if extSeen[xd.Number()] {
		return extSeen, w.d.errf("duplicate field %q", key)
	}
	extSeen[xd.Number()] = true

	w.d.skipWhitespace()
	if w.d.off < len(w.d.data) && w.d.data[w.d.off] == 'n' && !allowsNull(xd) {
		return extSeen, w.d.consumeLiteral("null")
	}

	if f := p.ty.ByDescriptor(xd); f.IsValid() {
		// Compiled with extension support: write the field directly.
		df := extDField(p, xd, f)
		return extSeen, w.field(df, m)
	}

	// Not compiled in: transcode the value to wire and stash it as an
	// unknown field, which is what the wire parser would have done.
	tc := transcoderPool.Get().(*transcoder) //nolint:errcheck
	tc.d = w.d
	tc.opts = w.opts
	tc.seen = tc.seen[:0]
	wire, err := tc.field(classifyU(xd, map[protoreflect.MessageDescriptor]*uplan{}), nil)
	w.d = tc.d
	transcoderPool.Put(tc)
	if err != nil {
		return extSeen, err
	}
	r := w.append(wire)
	cold := m.MutableCold()
	cold.Unknown = cold.Unknown.AppendOne(w.arena, r)
	return extSeen, nil
}

// extDFields caches direct plans for extension fields, keyed per plan.
var extDFields sync.Map // extKey -> *dfield

type extKey struct {
	p  *dplan
	xd protoreflect.FieldDescriptor
}

func extDField(p *dplan, xd protoreflect.FieldDescriptor, f *tdp.Field) *dfield {
	k := extKey{p, xd}
	if df, ok := extDFields.Load(k); ok {
		return df.(*dfield) //nolint:errcheck
	}
	dplanMu.Lock()
	built := make(map[*tdp.Type]*dplan)
	df := &dfield{}
	classifyD(df, xd, f, built)
	df.oneof = -1
	for kk, v := range built {
		dplanCache.Store(kk, v)
	}
	dplanMu.Unlock()
	extDFields.Store(k, df)
	return df
}
