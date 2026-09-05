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
	"math"
	"slices"
	"strconv"
	"strings"
	"sync"
	"unsafe"

	"google.golang.org/protobuf/reflect/protoreflect"

	"buf.build/go/hyperpb/internal/tdp"
	"buf.build/go/hyperpb/internal/tdp/dynamic"
	"buf.build/go/hyperpb/internal/tdp/empty"
	"buf.build/go/hyperpb/internal/xprotoreflect"
	"buf.build/go/hyperpb/internal/xunsafe"
)

// Value kind codes for the compiled marshal plan. Classifying fields once at
// plan-compile time keeps descriptor interface calls out of the per-message
// hot loop.
const (
	mkBool = iota
	mkInt32
	mkInt64
	mkUint32
	mkUint64
	mkFloat
	mkDouble
	mkString
	mkBytes
	mkEnum
	mkNullEnum // google.protobuf.NullValue
	mkMessage
	mkWKT // message whose type has a custom protojson shape
)

// Field shape codes.
const (
	msSingular = iota
	msList
	msMap
)

// Map key classes, for typed sorting.
const (
	mcString = iota
	mcInt
	mcUint
	mcBool
)

// mfield is one field of a compiled marshal plan.
type mfield struct {
	// name is the pre-rendered object member prefix `,"jsonName":`. The
	// leading comma is sliced off for the first member emitted.
	name string

	kind  uint8
	shape uint8

	// offset of the field's storage, used to skip unset composite fields
	// (whose storage begins with a nil pointer) without calling the getter
	// thunk at all.
	offset tdp.Offset

	// For maps.
	keyClass uint8
	valKind  uint8
	valEnums map[protoreflect.EnumNumber]string

	// For enums: number -> pre-rendered `"NAME"`.
	enums map[protoreflect.EnumNumber]string
}

type mplan struct {
	wkt    bool // type has a custom WKT shape; use the slow wkt path
	fields []mfield
}

// mplans caches compiled marshal plans, indexed by UseProtoNames.
var mplans [2]sync.Map // *tdp.Type -> *mplan

// Type identity words for fast protoreflect.Value unwrapping.
var (
	emptyMsgType = xunsafe.AnyType(empty.Message{})
)

// hyperMessageType returns the type identity of the boxed message value that
// hyperpb getter thunks produce (*hyperpb.Message). Resolved lazily through
// dynamic.Message.ProtoReflect, which routes through the linknamed
// constructor in the root package, because the concrete type is not visible
// from here.
var hyperMessageType = sync.OnceValue(func() uintptr {
	return xunsafe.AnyType((*dynamic.Message)(nil).ProtoReflect())
})

func mplanFor(ty *tdp.Type, protoNames bool) *mplan {
	idx := 0
	if protoNames {
		idx = 1
	}
	if p, ok := mplans[idx].Load(ty); ok {
		return p.(*mplan) //nolint:errcheck
	}
	p := compileMPlan(ty, protoNames)
	actual, _ := mplans[idx].LoadOrStore(ty, p)
	return actual.(*mplan) //nolint:errcheck
}

func compileMPlan(ty *tdp.Type, protoNames bool) *mplan {
	md := ty.Descriptor
	if isCustomWKT(md.FullName()) {
		return &mplan{wkt: true}
	}

	fds := ty.FieldDescriptors
	p := &mplan{fields: make([]mfield, len(fds))}
	for i, fd := range fds {
		pf := &p.fields[i]
		pf.offset = ty.ByIndex(i).Offset

		var name string
		switch {
		case fd.IsExtension():
			name = "[" + string(fd.FullName()) + "]"
		case protoNames:
			name = string(fd.Name())
		default:
			name = fd.JSONName()
		}
		pf.name = renderName(name)

		switch {
		case fd.IsMap():
			pf.shape = msMap
			switch fd.MapKey().Kind() {
			case protoreflect.StringKind:
				pf.keyClass = mcString
			case protoreflect.BoolKind:
				pf.keyClass = mcBool
			case protoreflect.Uint32Kind, protoreflect.Fixed32Kind,
				protoreflect.Uint64Kind, protoreflect.Fixed64Kind:
				pf.keyClass = mcUint
			default:
				pf.keyClass = mcInt
			}
			pf.valKind, pf.valEnums = classifyScalar(fd.MapValue())
		case fd.IsList():
			pf.shape = msList
			pf.kind, pf.enums = classifyScalar(fd)
		default:
			pf.shape = msSingular
			pf.kind, pf.enums = classifyScalar(fd)
		}
	}
	return p
}

// renderName pre-renders an object member prefix `,"name":` with JSON
// escaping applied once at compile time.
func renderName(name string) string {
	e := encoder{buf: make([]byte, 0, len(name)+4)}
	e.rawByte(',')
	e.str(name)
	e.rawByte(':')
	return string(e.buf)
}

func classifyScalar(fd protoreflect.FieldDescriptor) (uint8, map[protoreflect.EnumNumber]string) {
	switch fd.Kind() {
	case protoreflect.BoolKind:
		return mkBool, nil
	case protoreflect.Int32Kind, protoreflect.Sint32Kind, protoreflect.Sfixed32Kind:
		return mkInt32, nil
	case protoreflect.Int64Kind, protoreflect.Sint64Kind, protoreflect.Sfixed64Kind:
		return mkInt64, nil
	case protoreflect.Uint32Kind, protoreflect.Fixed32Kind:
		return mkUint32, nil
	case protoreflect.Uint64Kind, protoreflect.Fixed64Kind:
		return mkUint64, nil
	case protoreflect.FloatKind:
		return mkFloat, nil
	case protoreflect.DoubleKind:
		return mkDouble, nil
	case protoreflect.StringKind:
		return mkString, nil
	case protoreflect.BytesKind:
		return mkBytes, nil
	case protoreflect.EnumKind:
		ed := fd.Enum()
		if ed.FullName() == wktNullValue {
			return mkNullEnum, nil
		}
		vals := ed.Values()
		enums := make(map[protoreflect.EnumNumber]string, vals.Len())
		for i := range vals.Len() {
			vd := vals.Get(i)
			enums[vd.Number()] = `"` + string(vd.Name()) + `"`
		}
		return mkEnum, enums
	default: // MessageKind, GroupKind
		if isCustomWKT(fd.Message().FullName()) {
			return mkWKT, nil
		}
		return mkMessage, nil
	}
}

// rawString unwraps a string-typed Value without a type check; the plan
// already established the kind.
func rawString(v protoreflect.Value) string {
	n := xprotoreflect.GetRawInt(v)
	if n == 0 {
		return ""
	}
	return unsafe.String((*byte)(xprotoreflect.GetRawPointer(v)), n)
}

// rawBytes is rawString for bytes-typed Values.
func rawBytes(v protoreflect.Value) []byte {
	n := xprotoreflect.GetRawInt(v)
	if n == 0 {
		return nil
	}
	return unsafe.Slice((*byte)(xprotoreflect.GetRawPointer(v)), n)
}

// fastMessage marshals a hyperpb message through its compiled plan.
func (m *marshaler) fastMessage(dm *dynamic.Message) error {
	// A mutated message, multiline formatted output, or unpopulated/default
	// field emission routes through the reflection surface. Clean messages
	// pay one nil map check.
	if m.opts.Multiline || m.opts.EmitUnpopulated || m.opts.EmitDefaultValues {
		return m.overlayMessage(dm.ProtoReflect())
	}
	if dm.Shared != nil && dm.Shared.Overlays != nil {
		if _, ok := dm.Shared.Overlays[dm]; ok {
			return m.overlayMessage(dm.ProtoReflect())
		}
	}

	ty := dm.Type()
	p := mplanFor(ty, m.opts.UseProtoNames)
	if p.wkt {
		return m.wkt(dm.ProtoReflect(), ty.Descriptor)
	}

	e := m.e
	e.rawByte('{')
	if _, err := m.planFields(dm, p, true); err != nil {
		return err
	}
	e.rawByte('}')
	return nil
}

// planFields writes the fields of a message using its compiled plan.
func (m *marshaler) planFields(dm *dynamic.Message, p *mplan, first bool) (bool, error) {
	e := m.e
	ty := dm.Type()
	f := ty.ByIndex(0)
	for i := range p.fields {
		pf := &p.fields[i]

		// Composite fields' storage starts with a pointer that is nil while
		// the field is unset; checking it directly skips the getter thunk,
		// which matters for types with many mostly-unset map/list fields.
		if pf.shape != msSingular && dynamic.LoadField[unsafe.Pointer](dm, pf.offset) == nil {
			f = xunsafe.Add(f, 1)
			continue
		}

		v := f.Get(unsafe.Pointer(dm))
		f = xunsafe.Add(f, 1)
		if !v.IsValid() {
			continue
		}

		switch pf.shape {
		case msList:
			list := xprotoreflect.List(v)
			n := list.Len()
			if n == 0 {
				continue
			}
			first = m.member(pf, first)
			e.rawByte('[')
			for j := range n {
				if j > 0 {
					e.rawByte(',')
				}
				if err := m.fastScalar(pf.kind, pf.enums, list.Get(j)); err != nil {
					return first, err
				}
			}
			e.rawByte(']')

		case msMap:
			mp := xprotoreflect.Map(v)
			if mp.Len() == 0 {
				continue
			}
			first = m.member(pf, first)
			if err := m.fastMap(pf, mp); err != nil {
				return first, err
			}

		default:
			if pf.kind == mkMessage || pf.kind == mkWKT {
				if xprotoreflect.UnsafeUnwrap(v, emptyMsgType) != nil {
					continue // Unset submessage.
				}
			}
			first = m.member(pf, first)
			if err := m.fastScalar(pf.kind, pf.enums, v); err != nil {
				return first, err
			}
		}
	}
	return first, nil
}

// member emits the pre-rendered `,"name":` prefix, dropping the comma for

// the first member.
func (m *marshaler) member(pf *mfield, first bool) bool {
	if first {
		m.e.raw(pf.name[1:])
		return false
	}
	m.e.raw(pf.name)
	return false
}

// fastScalar writes one scalar/message value classified by the plan.
func (m *marshaler) fastScalar(kind uint8, enums map[protoreflect.EnumNumber]string, v protoreflect.Value) error {
	e := m.e
	switch kind {
	case mkBool:
		e.boolean(xprotoreflect.GetRawInt(v) != 0)
	case mkInt32:
		e.int32(int32(xprotoreflect.GetRawInt(v)))
	case mkInt64:
		e.int64(int64(xprotoreflect.GetRawInt(v)))
	case mkUint32:
		e.uint32(uint32(xprotoreflect.GetRawInt(v)))
	case mkUint64:
		e.uint64(xprotoreflect.GetRawInt(v))
	case mkFloat:
		// protoreflect.Value stores float32 widened to float64 bits.
		e.float(math.Float64frombits(xprotoreflect.GetRawInt(v)), 32)
	case mkDouble:
		e.float(math.Float64frombits(xprotoreflect.GetRawInt(v)), 64)
	case mkString:
		e.str(rawString(v))
	case mkBytes:
		e.base64(rawBytes(v))
	case mkEnum:
		n := protoreflect.EnumNumber(xprotoreflect.GetRawInt(v))
		if !m.opts.UseEnumNumbers {
			if name, ok := enums[n]; ok {
				e.raw(name)
				break
			}
		}
		e.int32(int32(n))
	case mkNullEnum:
		e.raw("null")
	case mkMessage:
		if ptr := xprotoreflect.UnsafeUnwrap(v, hyperMessageType()); ptr != nil {
			return m.fastMessage((*dynamic.Message)(ptr))
		}
		return m.msgValue(xprotoreflect.GetMessage[protoreflect.Message](v))
	default: // mkWKT
		pm := xprotoreflect.GetMessage[protoreflect.Message](v)
		return m.wkt(pm, pm.Descriptor())
	}
	return nil
}

// fastMap marshals a map field with typed, stack-friendly key sorting.
func (m *marshaler) fastMap(pf *mfield, mp protoreflect.Map) error {
	e := m.e
	e.rawByte('{')
	var err error
	switch pf.keyClass {
	case mcString:
		pbuf := strEntryPool.Get().(*[]strEntry) //nolint:errcheck
		*pbuf = (*pbuf)[:0]
		mp.Range(func(k protoreflect.MapKey, v protoreflect.Value) bool {
			*pbuf = append(*pbuf, strEntry{rawString(k.Value()), v})
			return true
		})
		entries := *pbuf
		slices.SortFunc(entries, func(a, b strEntry) int { return strings.Compare(a.k, b.k) })
		for i := range entries {
			if i > 0 {
				e.rawByte(',')
			}
			e.str(entries[i].k)
			e.rawByte(':')
			if err = m.fastScalar(pf.valKind, pf.valEnums, entries[i].v); err != nil {
				strEntryPool.Put(pbuf)
				return err
			}
		}
		strEntryPool.Put(pbuf)

	case mcBool:
		var fv, tv protoreflect.Value
		mp.Range(func(k protoreflect.MapKey, v protoreflect.Value) bool {
			if k.Bool() {
				tv = v
			} else {
				fv = v
			}
			return true
		})
		first := true
		for _, kv := range [2]struct {
			name string
			v    protoreflect.Value
		}{{`"false":`, fv}, {`"true":`, tv}} {
			if !kv.v.IsValid() {
				continue
			}
			first = e.comma(first)
			e.raw(kv.name)
			if err = m.fastScalar(pf.valKind, pf.valEnums, kv.v); err != nil {
				return err
			}
		}

	default: // mcInt, mcUint
		pbuf := intEntryPool.Get().(*[]intEntry) //nolint:errcheck
		*pbuf = (*pbuf)[:0]
		isUint := pf.keyClass == mcUint
		mp.Range(func(k protoreflect.MapKey, v protoreflect.Value) bool {
			var keyVal int64
			if isUint {
				keyVal = int64(k.Uint())
			} else {
				keyVal = k.Int()
			}
			*pbuf = append(*pbuf, intEntry{keyVal, v})
			return true
		})
		entries := *pbuf
		slices.SortFunc(entries, func(a, b intEntry) int {
			ak, bk := a.k, b.k
			if isUint {
				// Shift into signed order so one comparison works for both.
				ak ^= math.MinInt64
				bk ^= math.MinInt64
			}
			switch {
			case ak < bk:
				return -1
			case ak > bk:
				return 1
			}
			return 0
		})
		for i := range entries {
			if i > 0 {
				e.rawByte(',')
			}
			e.rawByte('"')
			if isUint {
				e.buf = strconv.AppendUint(e.buf, uint64(entries[i].k), 10)
			} else {
				e.buf = strconv.AppendInt(e.buf, entries[i].k, 10)
			}
			e.raw(`":`)
			if err = m.fastScalar(pf.valKind, pf.valEnums, entries[i].v); err != nil {
				intEntryPool.Put(pbuf)
				return err
			}
		}
		intEntryPool.Put(pbuf)
	}
	e.rawByte('}')
	return nil
}

type strEntry struct {
	k string
	v protoreflect.Value
}

type intEntry struct {
	k int64
	v protoreflect.Value
}

var (
	strEntryPool = sync.Pool{New: func() any { b := make([]strEntry, 0, 16); return &b }}
	intEntryPool = sync.Pool{New: func() any { b := make([]intEntry, 0, 16); return &b }}
)
