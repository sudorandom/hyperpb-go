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

package structenc

import (
	"cmp"
	"math"
	"reflect"
	"slices"
	"unicode/utf8"
	"unsafe"

	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

const speculativeLength = 1

func appendSpeculativeLength(b []byte) ([]byte, int) {
	pos := len(b)
	b = append(b, 0)
	return b, pos
}

func finishSpeculativeLength(b []byte, pos int) []byte {
	mlen := len(b) - pos - speculativeLength
	if mlen < 0x80 {
		b[pos] = byte(mlen)
		return b
	}
	msiz := protowire.SizeVarint(uint64(mlen))
	var zeros [8]byte
	b = append(b, zeros[:msiz-speculativeLength]...)
	copy(b[pos+msiz:], b[pos+speculativeLength:])
	b = b[:pos+msiz+mlen]
	protowire.AppendVarint(b[:pos], uint64(mlen))
	return b
}

// =============================================================================
// --- Singular Non-Optional Scalars (Proto3) ---
// =============================================================================

func encodeInt32(b []byte, base unsafe.Pointer, f *fieldEntry, _ Options) ([]byte, error) {
	v := *(*int32)(unsafe.Add(base, f.offset))
	if v != 0 {
		b = append(b, f.tagBytes[:f.tagLen]...)
		b = protowire.AppendVarint(b, uint64(v))
	}
	return b, nil
}

func encodeSint32(b []byte, base unsafe.Pointer, f *fieldEntry, _ Options) ([]byte, error) {
	v := *(*int32)(unsafe.Add(base, f.offset))
	if v != 0 {
		b = append(b, f.tagBytes[:f.tagLen]...)
		b = protowire.AppendVarint(b, protowire.EncodeZigZag(int64(v)))
	}
	return b, nil
}

func encodeUint32(b []byte, base unsafe.Pointer, f *fieldEntry, _ Options) ([]byte, error) {
	v := *(*uint32)(unsafe.Add(base, f.offset))
	if v != 0 {
		b = append(b, f.tagBytes[:f.tagLen]...)
		b = protowire.AppendVarint(b, uint64(v))
	}
	return b, nil
}

func encodeInt64(b []byte, base unsafe.Pointer, f *fieldEntry, _ Options) ([]byte, error) {
	v := *(*int64)(unsafe.Add(base, f.offset))
	if v != 0 {
		b = append(b, f.tagBytes[:f.tagLen]...)
		b = protowire.AppendVarint(b, uint64(v))
	}
	return b, nil
}

func encodeSint64(b []byte, base unsafe.Pointer, f *fieldEntry, _ Options) ([]byte, error) {
	v := *(*int64)(unsafe.Add(base, f.offset))
	if v != 0 {
		b = append(b, f.tagBytes[:f.tagLen]...)
		b = protowire.AppendVarint(b, protowire.EncodeZigZag(v))
	}
	return b, nil
}

func encodeUint64(b []byte, base unsafe.Pointer, f *fieldEntry, _ Options) ([]byte, error) {
	v := *(*uint64)(unsafe.Add(base, f.offset))
	if v != 0 {
		b = append(b, f.tagBytes[:f.tagLen]...)
		b = protowire.AppendVarint(b, v)
	}
	return b, nil
}

func encodeBool(b []byte, base unsafe.Pointer, f *fieldEntry, _ Options) ([]byte, error) {
	v := *(*bool)(unsafe.Add(base, f.offset))
	if v {
		b = append(b, f.tagBytes[:f.tagLen]...)
		b = append(b, 1)
	}
	return b, nil
}

func encodeFixed32(b []byte, base unsafe.Pointer, f *fieldEntry, _ Options) ([]byte, error) {
	v := *(*uint32)(unsafe.Add(base, f.offset))
	if v != 0 {
		b = append(b, f.tagBytes[:f.tagLen]...)
		b = protowire.AppendFixed32(b, v)
	}
	return b, nil
}

func encodeFixed64(b []byte, base unsafe.Pointer, f *fieldEntry, _ Options) ([]byte, error) {
	v := *(*uint64)(unsafe.Add(base, f.offset))
	if v != 0 {
		b = append(b, f.tagBytes[:f.tagLen]...)
		b = protowire.AppendFixed64(b, v)
	}
	return b, nil
}

func encodeFloat32(b []byte, base unsafe.Pointer, f *fieldEntry, _ Options) ([]byte, error) {
	v := *(*uint32)(unsafe.Add(base, f.offset))
	if v != 0 {
		b = append(b, f.tagBytes[:f.tagLen]...)
		b = protowire.AppendFixed32(b, v)
	}
	return b, nil
}

func encodeFloat64(b []byte, base unsafe.Pointer, f *fieldEntry, _ Options) ([]byte, error) {
	v := *(*uint64)(unsafe.Add(base, f.offset))
	if v != 0 {
		b = append(b, f.tagBytes[:f.tagLen]...)
		b = protowire.AppendFixed64(b, v)
	}
	return b, nil
}

func encodeString(b []byte, base unsafe.Pointer, f *fieldEntry, opts Options) ([]byte, error) {
	v := *(*string)(unsafe.Add(base, f.offset))
	if len(v) > 0 {
		if !opts.AllowInvalidUTF8 && !utf8.ValidString(v) {
			return nil, ErrInvalidUTF8
		}
		b = append(b, f.tagBytes[:f.tagLen]...)
		b = protowire.AppendString(b, v)
	}
	return b, nil
}

func encodeBytes(b []byte, base unsafe.Pointer, f *fieldEntry, _ Options) ([]byte, error) {
	v := *(*[]byte)(unsafe.Add(base, f.offset))
	if len(v) > 0 {
		b = append(b, f.tagBytes[:f.tagLen]...)
		b = protowire.AppendBytes(b, v)
	}
	return b, nil
}

// =============================================================================
// --- Singular Optional Scalars (Pointers in Proto2 / Proto3 Optional) ---
// =============================================================================

func encodeOptInt32(b []byte, base unsafe.Pointer, f *fieldEntry, _ Options) ([]byte, error) {
	p := *(*unsafe.Pointer)(unsafe.Add(base, f.offset))
	if p != nil {
		b = append(b, f.tagBytes[:f.tagLen]...)
		b = protowire.AppendVarint(b, uint64(*(*int32)(p)))
	}
	return b, nil
}

func encodeOptSint32(b []byte, base unsafe.Pointer, f *fieldEntry, _ Options) ([]byte, error) {
	p := *(*unsafe.Pointer)(unsafe.Add(base, f.offset))
	if p != nil {
		b = append(b, f.tagBytes[:f.tagLen]...)
		b = protowire.AppendVarint(b, protowire.EncodeZigZag(int64(*(*int32)(p))))
	}
	return b, nil
}

func encodeOptUint32(b []byte, base unsafe.Pointer, f *fieldEntry, _ Options) ([]byte, error) {
	p := *(*unsafe.Pointer)(unsafe.Add(base, f.offset))
	if p != nil {
		b = append(b, f.tagBytes[:f.tagLen]...)
		b = protowire.AppendVarint(b, uint64(*(*uint32)(p)))
	}
	return b, nil
}

func encodeOptInt64(b []byte, base unsafe.Pointer, f *fieldEntry, _ Options) ([]byte, error) {
	p := *(*unsafe.Pointer)(unsafe.Add(base, f.offset))
	if p != nil {
		b = append(b, f.tagBytes[:f.tagLen]...)
		b = protowire.AppendVarint(b, uint64(*(*int64)(p)))
	}
	return b, nil
}

func encodeOptSint64(b []byte, base unsafe.Pointer, f *fieldEntry, _ Options) ([]byte, error) {
	p := *(*unsafe.Pointer)(unsafe.Add(base, f.offset))
	if p != nil {
		b = append(b, f.tagBytes[:f.tagLen]...)
		b = protowire.AppendVarint(b, protowire.EncodeZigZag(*(*int64)(p)))
	}
	return b, nil
}

func encodeOptUint64(b []byte, base unsafe.Pointer, f *fieldEntry, _ Options) ([]byte, error) {
	p := *(*unsafe.Pointer)(unsafe.Add(base, f.offset))
	if p != nil {
		b = append(b, f.tagBytes[:f.tagLen]...)
		b = protowire.AppendVarint(b, *(*uint64)(p))
	}
	return b, nil
}

func encodeOptBool(b []byte, base unsafe.Pointer, f *fieldEntry, _ Options) ([]byte, error) {
	p := *(*unsafe.Pointer)(unsafe.Add(base, f.offset))
	if p != nil {
		b = append(b, f.tagBytes[:f.tagLen]...)
		if *(*bool)(p) {
			b = append(b, 1)
		} else {
			b = append(b, 0)
		}
	}
	return b, nil
}

func encodeOptFixed32(b []byte, base unsafe.Pointer, f *fieldEntry, _ Options) ([]byte, error) {
	p := *(*unsafe.Pointer)(unsafe.Add(base, f.offset))
	if p != nil {
		b = append(b, f.tagBytes[:f.tagLen]...)
		b = protowire.AppendFixed32(b, *(*uint32)(p))
	}
	return b, nil
}

func encodeOptFixed64(b []byte, base unsafe.Pointer, f *fieldEntry, _ Options) ([]byte, error) {
	p := *(*unsafe.Pointer)(unsafe.Add(base, f.offset))
	if p != nil {
		b = append(b, f.tagBytes[:f.tagLen]...)
		b = protowire.AppendFixed64(b, *(*uint64)(p))
	}
	return b, nil
}

func encodeOptFloat32(b []byte, base unsafe.Pointer, f *fieldEntry, _ Options) ([]byte, error) {
	p := *(*unsafe.Pointer)(unsafe.Add(base, f.offset))
	if p != nil {
		b = append(b, f.tagBytes[:f.tagLen]...)
		b = protowire.AppendFixed32(b, *(*uint32)(p))
	}
	return b, nil
}

func encodeOptFloat64(b []byte, base unsafe.Pointer, f *fieldEntry, _ Options) ([]byte, error) {
	p := *(*unsafe.Pointer)(unsafe.Add(base, f.offset))
	if p != nil {
		b = append(b, f.tagBytes[:f.tagLen]...)
		b = protowire.AppendFixed64(b, *(*uint64)(p))
	}
	return b, nil
}

func encodeOptString(b []byte, base unsafe.Pointer, f *fieldEntry, opts Options) ([]byte, error) {
	p := *(*unsafe.Pointer)(unsafe.Add(base, f.offset))
	if p != nil {
		v := *(*string)(p)
		if !opts.AllowInvalidUTF8 && !utf8.ValidString(v) {
			return nil, ErrInvalidUTF8
		}
		b = append(b, f.tagBytes[:f.tagLen]...)
		b = protowire.AppendString(b, v)
	}
	return b, nil
}

func encodeOptBytes(b []byte, base unsafe.Pointer, f *fieldEntry, _ Options) ([]byte, error) {
	v := *(*[]byte)(unsafe.Add(base, f.offset))
	if v != nil {
		b = append(b, f.tagBytes[:f.tagLen]...)
		b = protowire.AppendBytes(b, v)
	}
	return b, nil
}

// =============================================================================
// --- Submessages & Groups ---
// =============================================================================

func encodeSubMessage(b []byte, base unsafe.Pointer, f *fieldEntry, opts Options) ([]byte, error) {
	p := *(*unsafe.Pointer)(unsafe.Add(base, f.offset))
	if p == nil {
		return b, nil
	}
	b = append(b, f.tagBytes[:f.tagLen]...)
	var pos int
	b, pos = appendSpeculativeLength(b)
	var err error
	b, err = f.subEnc.Encode(p, b, opts)
	if err != nil {
		return nil, err
	}
	b = finishSpeculativeLength(b, pos)
	return b, nil
}

func encodeGroup(b []byte, base unsafe.Pointer, f *fieldEntry, opts Options) ([]byte, error) {
	p := *(*unsafe.Pointer)(unsafe.Add(base, f.offset))
	if p == nil {
		return b, nil
	}
	b = append(b, f.tagBytes[:f.tagLen]...)
	var err error
	b, err = f.subEnc.Encode(p, b, opts)
	if err != nil {
		return nil, err
	}
	b = protowire.AppendVarint(b, protowire.EncodeTag(f.num, protowire.EndGroupType))
	return b, nil
}

// =============================================================================
// --- Repeated Fields (Packed) ---
// =============================================================================

func encodePackedInt32(b []byte, base unsafe.Pointer, f *fieldEntry, _ Options) ([]byte, error) {
	s := *(*[]int32)(unsafe.Add(base, f.offset))
	if len(s) == 0 {
		return b, nil
	}
	b = append(b, f.tagBytes[:f.tagLen]...)
	b, pos := appendSpeculativeLength(b)
	for _, v := range s {
		b = protowire.AppendVarint(b, uint64(v))
	}
	return finishSpeculativeLength(b, pos), nil
}

func encodePackedSint32(b []byte, base unsafe.Pointer, f *fieldEntry, _ Options) ([]byte, error) {
	s := *(*[]int32)(unsafe.Add(base, f.offset))
	if len(s) == 0 {
		return b, nil
	}
	b = append(b, f.tagBytes[:f.tagLen]...)
	b, pos := appendSpeculativeLength(b)
	for _, v := range s {
		b = protowire.AppendVarint(b, protowire.EncodeZigZag(int64(v)))
	}
	return finishSpeculativeLength(b, pos), nil
}

func encodePackedUint32(b []byte, base unsafe.Pointer, f *fieldEntry, _ Options) ([]byte, error) {
	s := *(*[]uint32)(unsafe.Add(base, f.offset))
	if len(s) == 0 {
		return b, nil
	}
	b = append(b, f.tagBytes[:f.tagLen]...)
	b, pos := appendSpeculativeLength(b)
	for _, v := range s {
		b = protowire.AppendVarint(b, uint64(v))
	}
	return finishSpeculativeLength(b, pos), nil
}

func encodePackedInt64(b []byte, base unsafe.Pointer, f *fieldEntry, _ Options) ([]byte, error) {
	s := *(*[]int64)(unsafe.Add(base, f.offset))
	if len(s) == 0 {
		return b, nil
	}
	b = append(b, f.tagBytes[:f.tagLen]...)
	b, pos := appendSpeculativeLength(b)
	for _, v := range s {
		b = protowire.AppendVarint(b, uint64(v))
	}
	return finishSpeculativeLength(b, pos), nil
}

func encodePackedSint64(b []byte, base unsafe.Pointer, f *fieldEntry, _ Options) ([]byte, error) {
	s := *(*[]int64)(unsafe.Add(base, f.offset))
	if len(s) == 0 {
		return b, nil
	}
	b = append(b, f.tagBytes[:f.tagLen]...)
	b, pos := appendSpeculativeLength(b)
	for _, v := range s {
		b = protowire.AppendVarint(b, protowire.EncodeZigZag(v))
	}
	return finishSpeculativeLength(b, pos), nil
}

func encodePackedUint64(b []byte, base unsafe.Pointer, f *fieldEntry, _ Options) ([]byte, error) {
	s := *(*[]uint64)(unsafe.Add(base, f.offset))
	if len(s) == 0 {
		return b, nil
	}
	b = append(b, f.tagBytes[:f.tagLen]...)
	b, pos := appendSpeculativeLength(b)
	for _, v := range s {
		b = protowire.AppendVarint(b, v)
	}
	return finishSpeculativeLength(b, pos), nil
}

func encodePackedBool(b []byte, base unsafe.Pointer, f *fieldEntry, _ Options) ([]byte, error) {
	s := *(*[]bool)(unsafe.Add(base, f.offset))
	if len(s) == 0 {
		return b, nil
	}
	b = append(b, f.tagBytes[:f.tagLen]...)
	b, pos := appendSpeculativeLength(b)
	for _, v := range s {
		if v {
			b = append(b, 1)
		} else {
			b = append(b, 0)
		}
	}
	return finishSpeculativeLength(b, pos), nil
}

func encodePackedFixed32(b []byte, base unsafe.Pointer, f *fieldEntry, _ Options) ([]byte, error) {
	s := *(*[]uint32)(unsafe.Add(base, f.offset))
	if len(s) == 0 {
		return b, nil
	}
	b = append(b, f.tagBytes[:f.tagLen]...)
	b, pos := appendSpeculativeLength(b)
	for _, v := range s {
		b = protowire.AppendFixed32(b, v)
	}
	return finishSpeculativeLength(b, pos), nil
}

func encodePackedFixed64(b []byte, base unsafe.Pointer, f *fieldEntry, _ Options) ([]byte, error) {
	s := *(*[]uint64)(unsafe.Add(base, f.offset))
	if len(s) == 0 {
		return b, nil
	}
	b = append(b, f.tagBytes[:f.tagLen]...)
	b, pos := appendSpeculativeLength(b)
	for _, v := range s {
		b = protowire.AppendFixed64(b, v)
	}
	return finishSpeculativeLength(b, pos), nil
}

// =============================================================================
// --- Repeated Fields (Unpacked) ---
// =============================================================================

func encodeRepeatedString(b []byte, base unsafe.Pointer, f *fieldEntry, opts Options) ([]byte, error) {
	s := *(*[]string)(unsafe.Add(base, f.offset))
	for _, str := range s {
		if !opts.AllowInvalidUTF8 && !utf8.ValidString(str) {
			return nil, ErrInvalidUTF8
		}
		b = append(b, f.tagBytes[:f.tagLen]...)
		b = protowire.AppendString(b, str)
	}
	return b, nil
}

func encodeRepeatedBytes(b []byte, base unsafe.Pointer, f *fieldEntry, _ Options) ([]byte, error) {
	s := *(*[][]byte)(unsafe.Add(base, f.offset))
	for _, bs := range s {
		b = append(b, f.tagBytes[:f.tagLen]...)
		b = protowire.AppendBytes(b, bs)
	}
	return b, nil
}

func encodeRepeatedSubMessage(b []byte, base unsafe.Pointer, f *fieldEntry, opts Options) ([]byte, error) {
	s := *(*[]unsafe.Pointer)(unsafe.Add(base, f.offset))
	for _, p := range s {
		if p == nil {
			continue
		}
		b = append(b, f.tagBytes[:f.tagLen]...)
		var pos int
		b, pos = appendSpeculativeLength(b)
		var err error
		b, err = f.subEnc.Encode(p, b, opts)
		if err != nil {
			return nil, err
		}
		b = finishSpeculativeLength(b, pos)
	}
	return b, nil
}

// =============================================================================
// --- Maps ---
// =============================================================================

type mapFieldEncoder struct {
	mapType   reflect.Type
	keyKind   protoreflect.Kind
	valKind   protoreflect.Kind
	valSubEnc *Encoder
}

func (m *mapFieldEncoder) encode(b []byte, base unsafe.Pointer, f *fieldEntry, opts Options) ([]byte, error) {
	mapPtr := unsafe.Add(base, f.offset)
	mapVal := reflect.NewAt(m.mapType, mapPtr).Elem()
	if mapVal.Len() == 0 {
		return b, nil
	}

	if opts.Deterministic {
		keys := mapVal.MapKeys()
		sortMapKeys(keys, m.keyKind)
		for _, k := range keys {
			v := mapVal.MapIndex(k)
			var err error
			b, err = m.encodeEntry(b, f, k, v, opts)
			if err != nil {
				return nil, err
			}
		}
		return b, nil
	}

	iter := mapVal.MapRange()
	for iter.Next() {
		var err error
		b, err = m.encodeEntry(b, f, iter.Key(), iter.Value(), opts)
		if err != nil {
			return nil, err
		}
	}
	return b, nil
}

func (m *mapFieldEncoder) encodeEntry(b []byte, f *fieldEntry, k, v reflect.Value, opts Options) ([]byte, error) {
	b = append(b, f.tagBytes[:f.tagLen]...)
	var pos int
	b, pos = appendSpeculativeLength(b)

	b = appendMapKey(b, m.keyKind, k)
	var err error
	b, err = appendMapVal(b, m.valKind, m.valSubEnc, v, opts)
	if err != nil {
		return nil, err
	}

	b = finishSpeculativeLength(b, pos)
	return b, nil
}

func sortMapKeys(keys []reflect.Value, kind protoreflect.Kind) {
	switch kind {
	case protoreflect.Int32Kind, protoreflect.Sint32Kind, protoreflect.Int64Kind, protoreflect.Sint64Kind:
		slices.SortFunc(keys, func(a, b reflect.Value) int {
			return cmp.Compare(a.Int(), b.Int())
		})
	case protoreflect.Uint32Kind, protoreflect.Uint64Kind, protoreflect.Fixed32Kind, protoreflect.Fixed64Kind:
		slices.SortFunc(keys, func(a, b reflect.Value) int {
			return cmp.Compare(a.Uint(), b.Uint())
		})
	case protoreflect.StringKind:
		slices.SortFunc(keys, func(a, b reflect.Value) int {
			return cmp.Compare(a.String(), b.String())
		})
	case protoreflect.BoolKind:
		slices.SortFunc(keys, func(a, b reflect.Value) int {
			ab, bb := a.Bool(), b.Bool()
			if !ab && bb {
				return -1
			}
			if ab && !bb {
				return 1
			}
			return 0
		})
	default:
	}
}

func appendMapKey(b []byte, kind protoreflect.Kind, k reflect.Value) []byte {
	b = protowire.AppendTag(b, 1, wireTypes[kind])
	switch kind {
	case protoreflect.Int32Kind:
		return protowire.AppendVarint(b, uint64(int32(k.Int())))
	case protoreflect.Sint32Kind:
		return protowire.AppendVarint(b, protowire.EncodeZigZag(int64(int32(k.Int()))))
	case protoreflect.Uint32Kind:
		return protowire.AppendVarint(b, uint64(uint32(k.Uint())))
	case protoreflect.Int64Kind:
		return protowire.AppendVarint(b, uint64(k.Int()))
	case protoreflect.Sint64Kind:
		return protowire.AppendVarint(b, protowire.EncodeZigZag(k.Int()))
	case protoreflect.Uint64Kind:
		return protowire.AppendVarint(b, k.Uint())
	case protoreflect.Fixed32Kind, protoreflect.Sfixed32Kind:
		return protowire.AppendFixed32(b, uint32(k.Uint()))
	case protoreflect.Fixed64Kind, protoreflect.Sfixed64Kind:
		return protowire.AppendFixed64(b, k.Uint())
	case protoreflect.BoolKind:
		if k.Bool() {
			return append(b, 1)
		}
		return append(b, 0)
	case protoreflect.StringKind:
		return protowire.AppendString(b, k.String())
	default:
		return b
	}
}

func appendMapVal(b []byte, kind protoreflect.Kind, valSubEnc *Encoder, v reflect.Value, opts Options) ([]byte, error) {
	b = protowire.AppendTag(b, 2, wireTypes[kind])
	switch kind {
	case protoreflect.Int32Kind, protoreflect.EnumKind:
		return protowire.AppendVarint(b, uint64(int32(v.Int()))), nil
	case protoreflect.Sint32Kind:
		return protowire.AppendVarint(b, protowire.EncodeZigZag(int64(int32(v.Int())))), nil
	case protoreflect.Uint32Kind:
		return protowire.AppendVarint(b, uint64(uint32(v.Uint()))), nil
	case protoreflect.Int64Kind:
		return protowire.AppendVarint(b, uint64(v.Int())), nil
	case protoreflect.Sint64Kind:
		return protowire.AppendVarint(b, protowire.EncodeZigZag(v.Int())), nil
	case protoreflect.Uint64Kind:
		return protowire.AppendVarint(b, v.Uint()), nil
	case protoreflect.BoolKind:
		if v.Bool() {
			return append(b, 1), nil
		}
		return append(b, 0), nil
	case protoreflect.Fixed32Kind, protoreflect.Sfixed32Kind:
		return protowire.AppendFixed32(b, uint32(v.Uint())), nil
	case protoreflect.Fixed64Kind, protoreflect.Sfixed64Kind:
		return protowire.AppendFixed64(b, v.Uint()), nil
	case protoreflect.FloatKind:
		return protowire.AppendFixed32(b, math.Float32bits(float32(v.Float()))), nil
	case protoreflect.DoubleKind:
		return protowire.AppendFixed64(b, math.Float64bits(v.Float())), nil
	case protoreflect.StringKind:
		str := v.String()
		if !opts.AllowInvalidUTF8 && !utf8.ValidString(str) {
			return nil, ErrInvalidUTF8
		}
		return protowire.AppendString(b, str), nil
	case protoreflect.BytesKind:
		return protowire.AppendBytes(b, v.Bytes()), nil
	case protoreflect.MessageKind:
		if v.IsNil() {
			return b, nil
		}
		var pos int
		b, pos = appendSpeculativeLength(b)
		var err error
		if valSubEnc != nil {
			b, err = valSubEnc.Encode(unsafe.Pointer(v.Pointer()), b, opts)
		} else {
			if pm, ok := v.Interface().(proto.Message); ok {
				var enc *Encoder
				enc, err = Get(pm)
				if err == nil {
					b, err = enc.EncodeMessage(pm, b, opts)
				}
			}
		}
		if err != nil {
			return nil, err
		}
		b = finishSpeculativeLength(b, pos)
		return b, nil
	default:
		return b, nil
	}
}

// =============================================================================
// --- Oneofs ---
// =============================================================================

type oneofFieldEncoder struct {
	fieldIndex int
	parentType reflect.Type
	variants   map[reflect.Type]*oneofVariant
}

type oneofVariant struct {
	num      protowire.Number
	tagBytes [10]byte
	tagLen   uint8
	offset   uintptr
	encode   encodeFunc
	subEnc   *Encoder
}

func (oe *oneofFieldEncoder) encode(b []byte, base unsafe.Pointer, _ *fieldEntry, opts Options) ([]byte, error) {
	val := reflect.NewAt(oe.parentType, base).Elem().Field(oe.fieldIndex)
	if val.IsNil() {
		return b, nil
	}
	elem := val.Elem()
	variant, ok := oe.variants[elem.Type()]
	if !ok || variant == nil {
		return b, nil
	}
	innerPtr := unsafe.Pointer(elem.Pointer())
	if innerPtr == nil {
		return b, nil
	}
	subEntry := &fieldEntry{
		num:      variant.num,
		tagBytes: variant.tagBytes,
		tagLen:   variant.tagLen,
		offset:   variant.offset,
		encode:   variant.encode,
		subEnc:   variant.subEnc,
	}
	return variant.encode(b, innerPtr, subEntry, opts)
}

// Oneof unconditional encoders (oneofs have presence even in proto3, so zero values are emitted):

func encodeOneofInt32(b []byte, base unsafe.Pointer, f *fieldEntry, _ Options) ([]byte, error) {
	v := *(*int32)(unsafe.Add(base, f.offset))
	b = append(b, f.tagBytes[:f.tagLen]...)
	b = protowire.AppendVarint(b, uint64(v))
	return b, nil
}

func encodeOneofSint32(b []byte, base unsafe.Pointer, f *fieldEntry, _ Options) ([]byte, error) {
	v := *(*int32)(unsafe.Add(base, f.offset))
	b = append(b, f.tagBytes[:f.tagLen]...)
	b = protowire.AppendVarint(b, protowire.EncodeZigZag(int64(v)))
	return b, nil
}

func encodeOneofUint32(b []byte, base unsafe.Pointer, f *fieldEntry, _ Options) ([]byte, error) {
	v := *(*uint32)(unsafe.Add(base, f.offset))
	b = append(b, f.tagBytes[:f.tagLen]...)
	b = protowire.AppendVarint(b, uint64(v))
	return b, nil
}

func encodeOneofInt64(b []byte, base unsafe.Pointer, f *fieldEntry, _ Options) ([]byte, error) {
	v := *(*int64)(unsafe.Add(base, f.offset))
	b = append(b, f.tagBytes[:f.tagLen]...)
	b = protowire.AppendVarint(b, uint64(v))
	return b, nil
}

func encodeOneofSint64(b []byte, base unsafe.Pointer, f *fieldEntry, _ Options) ([]byte, error) {
	v := *(*int64)(unsafe.Add(base, f.offset))
	b = append(b, f.tagBytes[:f.tagLen]...)
	b = protowire.AppendVarint(b, protowire.EncodeZigZag(v))
	return b, nil
}

func encodeOneofUint64(b []byte, base unsafe.Pointer, f *fieldEntry, _ Options) ([]byte, error) {
	v := *(*uint64)(unsafe.Add(base, f.offset))
	b = append(b, f.tagBytes[:f.tagLen]...)
	b = protowire.AppendVarint(b, v)
	return b, nil
}

func encodeOneofBool(b []byte, base unsafe.Pointer, f *fieldEntry, _ Options) ([]byte, error) {
	v := *(*bool)(unsafe.Add(base, f.offset))
	b = append(b, f.tagBytes[:f.tagLen]...)
	if v {
		b = append(b, 1)
	} else {
		b = append(b, 0)
	}
	return b, nil
}

func encodeOneofFixed32(b []byte, base unsafe.Pointer, f *fieldEntry, _ Options) ([]byte, error) {
	v := *(*uint32)(unsafe.Add(base, f.offset))
	b = append(b, f.tagBytes[:f.tagLen]...)
	b = protowire.AppendFixed32(b, v)
	return b, nil
}

func encodeOneofFixed64(b []byte, base unsafe.Pointer, f *fieldEntry, _ Options) ([]byte, error) {
	v := *(*uint64)(unsafe.Add(base, f.offset))
	b = append(b, f.tagBytes[:f.tagLen]...)
	b = protowire.AppendFixed64(b, v)
	return b, nil
}

func encodeOneofFloat32(b []byte, base unsafe.Pointer, f *fieldEntry, _ Options) ([]byte, error) {
	v := *(*uint32)(unsafe.Add(base, f.offset))
	b = append(b, f.tagBytes[:f.tagLen]...)
	b = protowire.AppendFixed32(b, v)
	return b, nil
}

func encodeOneofFloat64(b []byte, base unsafe.Pointer, f *fieldEntry, _ Options) ([]byte, error) {
	v := *(*uint64)(unsafe.Add(base, f.offset))
	b = append(b, f.tagBytes[:f.tagLen]...)
	b = protowire.AppendFixed64(b, v)
	return b, nil
}

func encodeOneofString(b []byte, base unsafe.Pointer, f *fieldEntry, opts Options) ([]byte, error) {
	v := *(*string)(unsafe.Add(base, f.offset))
	if !opts.AllowInvalidUTF8 && !utf8.ValidString(v) {
		return nil, ErrInvalidUTF8
	}
	b = append(b, f.tagBytes[:f.tagLen]...)
	b = protowire.AppendString(b, v)
	return b, nil
}

func encodeOneofBytes(b []byte, base unsafe.Pointer, f *fieldEntry, _ Options) ([]byte, error) {
	v := *(*[]byte)(unsafe.Add(base, f.offset))
	b = append(b, f.tagBytes[:f.tagLen]...)
	b = protowire.AppendBytes(b, v)
	return b, nil
}
