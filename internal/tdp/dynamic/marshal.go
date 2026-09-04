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

package dynamic

import (
	"fmt"
	"math"
	"slices"
	"unicode/utf8"
	"unsafe"

	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/reflect/protoreflect"

	"buf.build/go/hyperpb/internal/tdp"
	"buf.build/go/hyperpb/internal/xunsafe"
	"buf.build/go/hyperpb/internal/zc"
)

var wireTypes = [...]protowire.Type{
	0:                         0,
	protoreflect.BoolKind:     protowire.VarintType,
	protoreflect.EnumKind:     protowire.VarintType,
	protoreflect.Int32Kind:    protowire.VarintType,
	protoreflect.Sint32Kind:   protowire.VarintType,
	protoreflect.Uint32Kind:   protowire.VarintType,
	protoreflect.Int64Kind:    protowire.VarintType,
	protoreflect.Sint64Kind:   protowire.VarintType,
	protoreflect.Uint64Kind:   protowire.VarintType,
	protoreflect.Sfixed32Kind: protowire.Fixed32Type,
	protoreflect.Fixed32Kind:  protowire.Fixed32Type,
	protoreflect.FloatKind:    protowire.Fixed32Type,
	protoreflect.Sfixed64Kind: protowire.Fixed64Type,
	protoreflect.Fixed64Kind:  protowire.Fixed64Type,
	protoreflect.DoubleKind:   protowire.Fixed64Type,
	protoreflect.StringKind:   protowire.BytesType,
	protoreflect.BytesKind:    protowire.BytesType,
	protoreflect.MessageKind:  protowire.BytesType,
	protoreflect.GroupKind:    protowire.StartGroupType,
}

// =============================================================================
// --- Message Serialization (Marshal) ---
// =============================================================================

// MarshalMessage serializes the message into the given byte slice b, returning
// the appended slice. It prioritizes fast-path serialization for unmutated base
// fields directly from the arena using unsafe offset lookups, avoiding costly
// protoreflect.Value wrapping allocations.
func (m *Message) MarshalMessage(b []byte) ([]byte, error) {
	if m == nil {
		return b, nil
	}

	var ov *MessageOverlay
	if m.Shared != nil && m.Shared.Overlays != nil {
		ov = m.Shared.Overlays[m]
	}

	ty := m.Type()
	f := ty.ByIndex(0)
	i := 0

	// Track fields serialized from overlays or Cleared sets.
	var seenBuf [16]protoreflect.FieldNumber
	seen := seenBuf[:0]
	var seenMap map[protoreflect.FieldNumber]bool

	isSeen := func(num protoreflect.FieldNumber) bool {
		if seenMap != nil {
			return seenMap[num]
		}
		return slices.Contains(seen, num)
	}

	markSeen := func(num protoreflect.FieldNumber) {
		if seenMap != nil {
			seenMap[num] = true
			return
		}
		if len(seen) < 16 {
			seen = append(seen, num)
		} else {
			seenMap = make(map[protoreflect.FieldNumber]bool)
			for _, s := range seen {
				seenMap[s] = true
			}
			seenMap[num] = true
		}
	}

	for f.IsValid() {
		fd := ty.FieldDescriptors[i]
		num := fd.Number()

		// 1. Check if cleared
		if ov != nil && ov.Cleared[num] {
			markSeen(num)
			f = xunsafe.Add(f, 1)
			i++
			continue
		}

		// 2. Check overlay
		if ov != nil {
			if val, ok := ov.Fields[num]; ok {
				markSeen(num)
				var err error
				b, err = marshalReflectField(b, fd, val.val)
				if err != nil {
					return nil, err
				}
				f = xunsafe.Add(f, 1)
				i++
				continue
			}
		}

		// 3. Check fallback
		if ov != nil && ov.Fallback != nil {
			if ov.Fallback.Has(fd) {
				markSeen(num)
				var err error
				b, err = marshalReflectField(b, fd, ov.Fallback.Get(fd))
				if err != nil {
					return nil, err
				}
				f = xunsafe.Add(f, 1)
				i++
				continue
			}
		}

		// 4. Base message field (Fast non-allocating path)
		if m.HasNoAlloc(fd, f) {
			var err error
			b, err = m.marshalBaseField(b, fd, f)
			if err != nil {
				return nil, err
			}
		}

		f = xunsafe.Add(f, 1)
		i++
	}

	// 5. Extensions/extra fields in the overlay
	if ov != nil {
		for num, val := range ov.Fields {
			if !isSeen(num) && !ov.Cleared[num] {
				var err error
				b, err = marshalReflectField(b, val.fd, val.val)
				if err != nil {
					return nil, err
				}
			}
		}
	}

	// 6. Unknown fields
	if len(m.GetUnknown()) > 0 {
		b = append(b, m.GetUnknown()...)
	}

	return b, nil
}

func (m *Message) marshalBaseField(b []byte, fd protoreflect.FieldDescriptor, f *tdp.Field) ([]byte, error) {
	if fd.IsList() {
		v := f.Get(unsafe.Pointer(m))
		return marshalReflectList(b, fd, v.List())
	}
	if fd.IsMap() {
		v := f.Get(unsafe.Pointer(m))
		return marshalReflectMap(b, fd, v.Map())
	}
	if fd.Message() != nil {
		p := GetField[*Message](m, f.Offset)
		if p == nil || *p == nil {
			return b, nil
		}
		subMsg := *p

		if fd.Kind() == protoreflect.GroupKind {
			b = protowire.AppendTag(b, fd.Number(), protowire.StartGroupType)
			var err error
			b, err = subMsg.MarshalMessage(b)
			if err != nil {
				return nil, err
			}
			b = protowire.AppendVarint(b, protowire.EncodeTag(fd.Number(), protowire.EndGroupType))
			return b, nil
		}

		b = protowire.AppendTag(b, fd.Number(), protowire.BytesType)
		b, pos := appendSpeculativeLength(b)
		var err error
		b, err = subMsg.MarshalMessage(b)
		if err != nil {
			return nil, err
		}
		b = finishSpeculativeLength(b, pos)
		return b, nil
	}

	b = protowire.AppendTag(b, fd.Number(), wireTypes[fd.Kind()])

	switch fd.Kind() {
	case protoreflect.BoolKind:
		var val bool
		od := fd.ContainingOneof()
		switch {
		case od != nil && !od.IsSynthetic() && f.Offset.Number != 0:
			// A member of a oneof implemented as a real union: the value is
			// stored as a byte in the union storage, and Offset.Bit is the
			// byte offset of the which-word, not a bit index.
			val = *GetField[byte](m, f.Offset) != 0
		case fd.HasPresence():
			val = m.GetBit(f.Offset.Bit + 1)
		default:
			val = m.GetBit(f.Offset.Bit)
		}
		b = protowire.AppendVarint(b, protowire.EncodeBool(val))

	case protoreflect.EnumKind:
		val := *GetField[protoreflect.EnumNumber](m, f.Offset)
		b = protowire.AppendVarint(b, uint64(val))

	case protoreflect.Int32Kind:
		val := *GetField[int32](m, f.Offset)
		b = protowire.AppendVarint(b, uint64(val))

	case protoreflect.Sint32Kind:
		val := *GetField[int32](m, f.Offset)
		b = protowire.AppendVarint(b, protowire.EncodeZigZag(int64(val)))

	case protoreflect.Uint32Kind:
		val := *GetField[uint32](m, f.Offset)
		b = protowire.AppendVarint(b, uint64(val))

	case protoreflect.Int64Kind:
		val := *GetField[int64](m, f.Offset)
		b = protowire.AppendVarint(b, uint64(val))

	case protoreflect.Sint64Kind:
		val := *GetField[int64](m, f.Offset)
		b = protowire.AppendVarint(b, protowire.EncodeZigZag(val))

	case protoreflect.Uint64Kind:
		val := *GetField[uint64](m, f.Offset)
		b = protowire.AppendVarint(b, val)

	case protoreflect.Sfixed32Kind:
		val := *GetField[int32](m, f.Offset)
		b = protowire.AppendFixed32(b, uint32(val))

	case protoreflect.Fixed32Kind:
		val := *GetField[uint32](m, f.Offset)
		b = protowire.AppendFixed32(b, val)

	case protoreflect.FloatKind:
		val := *GetField[uint32](m, f.Offset)
		b = protowire.AppendFixed32(b, val)

	case protoreflect.Sfixed64Kind:
		val := *GetField[int64](m, f.Offset)
		b = protowire.AppendFixed64(b, uint64(val))

	case protoreflect.Fixed64Kind:
		val := *GetField[uint64](m, f.Offset)
		b = protowire.AppendFixed64(b, val)

	case protoreflect.DoubleKind:
		val := *GetField[uint64](m, f.Offset)
		b = protowire.AppendFixed64(b, val)

	case protoreflect.StringKind:
		r := *GetField[zc.Range](m, f.Offset)
		str := r.String(m.Shared.Src)
		if fd.Syntax() == protoreflect.Proto3 && !utf8.ValidString(str) {
			return b, fmt.Errorf("field %s contains invalid UTF-8", fd.FullName())
		}
		b = protowire.AppendString(b, str)

	case protoreflect.BytesKind:
		r := *GetField[zc.Range](m, f.Offset)
		b = protowire.AppendBytes(b, r.Bytes(m.Shared.Src))

	default:
		return b, fmt.Errorf("invalid kind %v", fd.Kind())
	}

	return b, nil
}

// =============================================================================
// --- Reflection-Based Fallback Serialization ---
// =============================================================================

func marshalMessageOrReflect(b []byte, m protoreflect.Message) ([]byte, error) {
	dm := unwrapMessage(m)
	if dm != nil {
		return dm.MarshalMessage(b)
	}
	return marshalReflectMessage(b, m)
}

func marshalReflectMessage(b []byte, m protoreflect.Message) ([]byte, error) {
	var err error
	m.Range(func(fd protoreflect.FieldDescriptor, v protoreflect.Value) bool {
		b, err = marshalReflectField(b, fd, v)
		return err == nil
	})
	if err != nil {
		return nil, err
	}
	if len(m.GetUnknown()) > 0 {
		b = append(b, m.GetUnknown()...)
	}
	return b, nil
}

func marshalReflectField(b []byte, fd protoreflect.FieldDescriptor, value protoreflect.Value) ([]byte, error) {
	switch {
	case fd.IsList():
		return marshalReflectList(b, fd, value.List())
	case fd.IsMap():
		return marshalReflectMap(b, fd, value.Map())
	default:
		b = protowire.AppendTag(b, fd.Number(), wireTypes[fd.Kind()])
		return marshalReflectSingular(b, fd, value)
	}
}

func marshalReflectList(b []byte, fd protoreflect.FieldDescriptor, list protoreflect.List) ([]byte, error) {
	if fd.IsPacked() && list.Len() > 0 {
		b = protowire.AppendTag(b, fd.Number(), protowire.BytesType)
		b, pos := appendSpeculativeLength(b)
		for i := range list.Len() {
			var err error
			b, err = marshalReflectSingular(b, fd, list.Get(i))
			if err != nil {
				return b, err
			}
		}
		b = finishSpeculativeLength(b, pos)
		return b, nil
	}

	kind := fd.Kind()
	for i := range list.Len() {
		var err error
		b = protowire.AppendTag(b, fd.Number(), wireTypes[kind])
		b, err = marshalReflectSingular(b, fd, list.Get(i))
		if err != nil {
			return b, err
		}
	}
	return b, nil
}

func marshalReflectMap(b []byte, fd protoreflect.FieldDescriptor, mapv protoreflect.Map) ([]byte, error) {
	keyf := fd.MapKey()
	valf := fd.MapValue()
	var err error
	mapv.Range(func(key protoreflect.MapKey, value protoreflect.Value) bool {
		b = protowire.AppendTag(b, fd.Number(), protowire.BytesType)
		var pos int
		b, pos = appendSpeculativeLength(b)

		b, err = marshalReflectField(b, keyf, key.Value())
		if err != nil {
			return false
		}
		b, err = marshalReflectField(b, valf, value)
		if err != nil {
			return false
		}
		b = finishSpeculativeLength(b, pos)
		return true
	})
	return b, err
}

func marshalReflectSingular(b []byte, fd protoreflect.FieldDescriptor, v protoreflect.Value) ([]byte, error) {
	switch fd.Kind() {
	case protoreflect.BoolKind:
		b = protowire.AppendVarint(b, protowire.EncodeBool(v.Bool()))
	case protoreflect.EnumKind:
		b = protowire.AppendVarint(b, uint64(v.Enum()))
	case protoreflect.Int32Kind:
		b = protowire.AppendVarint(b, uint64(int32(v.Int())))
	case protoreflect.Sint32Kind:
		b = protowire.AppendVarint(b, protowire.EncodeZigZag(int64(int32(v.Int()))))
	case protoreflect.Uint32Kind:
		b = protowire.AppendVarint(b, uint64(uint32(v.Uint())))
	case protoreflect.Int64Kind:
		b = protowire.AppendVarint(b, uint64(v.Int()))
	case protoreflect.Sint64Kind:
		b = protowire.AppendVarint(b, protowire.EncodeZigZag(v.Int()))
	case protoreflect.Uint64Kind:
		b = protowire.AppendVarint(b, v.Uint())
	case protoreflect.Sfixed32Kind:
		b = protowire.AppendFixed32(b, uint32(v.Int()))
	case protoreflect.Fixed32Kind:
		b = protowire.AppendFixed32(b, uint32(v.Uint()))
	case protoreflect.FloatKind:
		b = protowire.AppendFixed32(b, math.Float32bits(float32(v.Float())))
	case protoreflect.Sfixed64Kind:
		b = protowire.AppendFixed64(b, uint64(v.Int()))
	case protoreflect.Fixed64Kind:
		b = protowire.AppendFixed64(b, v.Uint())
	case protoreflect.DoubleKind:
		b = protowire.AppendFixed64(b, math.Float64bits(v.Float()))
	case protoreflect.StringKind:
		if fd.Syntax() == protoreflect.Proto3 && !utf8.ValidString(v.String()) {
			return b, fmt.Errorf("field %s contains invalid UTF-8", fd.FullName())
		}
		b = protowire.AppendString(b, v.String())
	case protoreflect.BytesKind:
		b = protowire.AppendBytes(b, v.Bytes())
	case protoreflect.MessageKind:
		var pos int
		var err error
		b, pos = appendSpeculativeLength(b)
		b, err = marshalMessageOrReflect(b, v.Message())
		if err != nil {
			return b, err
		}
		b = finishSpeculativeLength(b, pos)
	case protoreflect.GroupKind:
		var err error
		b, err = marshalMessageOrReflect(b, v.Message())
		if err != nil {
			return b, err
		}
		b = protowire.AppendVarint(b, protowire.EncodeTag(fd.Number(), protowire.EndGroupType))
	default:
		return b, fmt.Errorf("invalid kind %v", fd.Kind())
	}
	return b, nil
}

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
	for range msiz - speculativeLength {
		b = append(b, 0)
	}
	copy(b[pos+msiz:], b[pos+speculativeLength:])
	b = b[:pos+msiz+mlen]
	protowire.AppendVarint(b[:pos], uint64(mlen))
	return b
}
