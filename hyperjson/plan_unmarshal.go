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
	"sync"

	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// Value classes for the compiled unmarshal plan. Classifying once at compile
// time keeps descriptor interface calls out of the per-document hot loop.
const (
	ucBool = iota
	ucInt32
	ucSint32
	ucSfixed32
	ucInt64
	ucSint64
	ucSfixed64
	ucUint32
	ucFixed32
	ucUint64
	ucFixed64
	ucFloat
	ucDouble
	ucString
	ucBytes
	ucEnum
	ucMessage
	ucGroup
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

// classifyKind maps a descriptor kind to its value class and default wire type.
func classifyKind(k protoreflect.Kind) (uint8, protowire.Type) {
	switch k {
	case protoreflect.BoolKind:
		return ucBool, protowire.VarintType
	case protoreflect.Int32Kind:
		return ucInt32, protowire.VarintType
	case protoreflect.Sint32Kind:
		return ucSint32, protowire.VarintType
	case protoreflect.Sfixed32Kind:
		return ucSfixed32, protowire.Fixed32Type
	case protoreflect.Int64Kind:
		return ucInt64, protowire.VarintType
	case protoreflect.Sint64Kind:
		return ucSint64, protowire.VarintType
	case protoreflect.Sfixed64Kind:
		return ucSfixed64, protowire.Fixed64Type
	case protoreflect.Uint32Kind:
		return ucUint32, protowire.VarintType
	case protoreflect.Fixed32Kind:
		return ucFixed32, protowire.Fixed32Type
	case protoreflect.Uint64Kind:
		return ucUint64, protowire.VarintType
	case protoreflect.Fixed64Kind:
		return ucFixed64, protowire.Fixed64Type
	case protoreflect.FloatKind:
		return ucFloat, protowire.Fixed32Type
	case protoreflect.DoubleKind:
		return ucDouble, protowire.Fixed64Type
	case protoreflect.StringKind:
		return ucString, protowire.BytesType
	case protoreflect.BytesKind:
		return ucBytes, protowire.BytesType
	case protoreflect.EnumKind:
		return ucEnum, protowire.VarintType
	case protoreflect.GroupKind:
		return ucGroup, protowire.StartGroupType
	default: // MessageKind
		return ucMessage, protowire.BytesType
	}
}

func buildEnumMap(ed protoreflect.EnumDescriptor) map[string]protoreflect.EnumNumber {
	vals := ed.Values()
	enums := make(map[string]protoreflect.EnumNumber, vals.Len())
	for i := range vals.Len() {
		vd := vals.Get(i)
		enums[string(vd.Name())] = vd.Number()
	}
	return enums
}

func isNullEnum(fd protoreflect.FieldDescriptor) (nullEnum bool, allowsNull bool) {
	ed := fd.Enum()
	if ed.FullName() == wktNullValue {
		return true, !fd.IsList() && !fd.IsMap()
	}
	return false, false
}

// ufield is one field of a compiled unmarshal plan.
type ufield struct {

	class uint8
	wkt   uint8 // for ucMessage/ucGroup: the submessage's WKT code

	isList bool
	isMap  bool

	// null is a real value for this field (google.protobuf.Value fields and
	// NullValue enums), not "unset".
	allowsNull bool
	nullEnum   bool

	// idx is this field's position in the duplicate-detection bitset;
	// oneof is p.fields + the containing oneof's index, or -1.
	idx   uint32
	oneof int32

	num protowire.Number
	tag uint64 // precomputed key varint: num<<3 | wire type

	enums map[string]protoreflect.EnumNumber
	sub   *uplan  // message/group field types
	key   *ufield // map key (tag 1)
	val   *ufield // map value (tag 2)

	fd protoreflect.FieldDescriptor
}

// uplan is a compiled unmarshal plan for one message type.
type uplan struct {
	md     protoreflect.MessageDescriptor
	wkt    uint8
	byName map[string]*ufield
	// words is the size of the seen-bitset covering fields then oneofs.
	words   int
	fields  uint32
	wrapped *ufield // for wkWrapper: the "value" field

	// required is set when this type (or any transitively reachable message
	// type) has required fields, forcing an Initialized check after parse.
	required bool
}

var (
	uplanCache sync.Map // protoreflect.MessageDescriptor -> *uplan
	uplanMu    sync.Mutex
)

func uplanFor(md protoreflect.MessageDescriptor) *uplan {
	if p, ok := uplanCache.Load(md); ok {
		return p.(*uplan) //nolint:errcheck
	}
	uplanMu.Lock()
	defer uplanMu.Unlock()
	if p, ok := uplanCache.Load(md); ok {
		return p.(*uplan) //nolint:errcheck
	}
	// Compile the whole strongly-connected component under the lock using a
	// scratch map for cycles, then publish only fully-built plans.
	built := make(map[protoreflect.MessageDescriptor]*uplan)
	p := buildUPlan(md, built)

	// Propagate the required-fields flag through message cycles to a
	// fixpoint before publishing.
	for changed := true; changed; {
		changed = false
		for _, v := range built {
			if v.required {
				continue
			}
			for _, uf := range v.byName {
				if uf.sub != nil && uf.sub.required {
					v.required = true
					changed = true
					break
				}
			}
		}
	}
	for k, v := range built {
		uplanCache.Store(k, v)
	}
	return p
}

func buildUPlan(md protoreflect.MessageDescriptor, built map[protoreflect.MessageDescriptor]*uplan) *uplan {
	if p, ok := uplanCache.Load(md); ok {
		return p.(*uplan) //nolint:errcheck
	}
	if p, ok := built[md]; ok {
		return p
	}
	p := &uplan{md: md, wkt: wktCode(md.FullName())}
	built[md] = p

	fds := md.Fields()
	oneofs := md.Oneofs()
	p.fields = uint32(fds.Len())
	p.words = (fds.Len() + oneofs.Len() + 63) / 64
	p.byName = make(map[string]*ufield, fds.Len()*2)
	for i := range fds.Len() {
		fd := fds.Get(i)
		if fd.Cardinality() == protoreflect.Required {
			p.required = true
		}
		uf := classifyU(fd, built)
		uf.idx = uint32(i)
		uf.oneof = -1
		if od := fd.ContainingOneof(); od != nil && !od.IsSynthetic() {
			uf.oneof = int32(p.fields) + int32(od.Index())
		}
		p.byName[fd.JSONName()] = uf
		p.byName[string(fd.Name())] = uf
	}
	if p.wkt == wkWrapper {
		p.wrapped = p.byName["value"]
	}
	return p
}

// wktCode maps a full name to its custom-shape code.
func wktCode(name protoreflect.FullName) uint8 {
	if !isCustomWKT(name) {
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
	default:
		return wkWrapper
	}
}

// classifyU builds a ufield for one descriptor (also used for extensions and
// map key/value pseudo-fields).
func classifyU(fd protoreflect.FieldDescriptor, built map[protoreflect.MessageDescriptor]*uplan) *ufield {
	uf := &ufield{fd: fd, num: fd.Number(), oneof: -1}

	switch {
	case fd.IsMap():
		uf.isMap = true
		uf.tag = uint64(protowire.EncodeTag(uf.num, protowire.BytesType))
		uf.key = classifyU(fd.MapKey(), built)
		uf.val = classifyU(fd.MapValue(), built)
		return uf
	case fd.IsList():
		uf.isList = true
	}

	var wire protowire.Type
	uf.class, wire = classifyKind(fd.Kind())
	switch fd.Kind() {
	case protoreflect.EnumKind:
		uf.nullEnum, uf.allowsNull = isNullEnum(fd)
		uf.enums = buildEnumMap(fd.Enum())
	case protoreflect.GroupKind:
		uf.sub = buildUPlan(fd.Message(), built)
		uf.wkt = uf.sub.wkt
	case protoreflect.MessageKind:
		uf.sub = buildUPlan(fd.Message(), built)
		uf.wkt = uf.sub.wkt
		uf.allowsNull = uf.wkt == wkValue && !uf.isList
	}
	uf.tag = uint64(protowire.EncodeTag(uf.num, wire))
	return uf
}

