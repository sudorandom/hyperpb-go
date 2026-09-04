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

	"google.golang.org/protobuf/reflect/protoreflect"

	"buf.build/go/hyperpb/internal/tdp"
)

// Presence disciplines for direct field writes, mirroring the archetypes in
// internal/tdp/thunks.
const (
	dpImplicit = iota // implicit presence: zero value means unset
	dpHasbit          // explicit presence: has-bit at Offset.Bit
	dpOneof           // union member: which-word (uint32) at byte offset Offset.Bit
	dpPointer         // message/group: non-nil pointer means present
)

// dfield is one field of a compiled direct-write plan. Unlike ufield it is
// bound to one compiled *tdp.Type's layout: the same descriptor compiled
// twice (e.g. after PGO recompilation) gets distinct dplans.
type dfield struct {
	class    uint8 // uc* value classes, shared with the transcode plan
	wkt      uint8 // for message classes: the submessage's WKT code
	presence uint8

	isList bool
	isMap  bool

	allowsNull bool
	nullEnum   bool

	idx   uint32 // duplicate-detection bitset position
	oneof int32  // bitset position of the containing oneof, or -1

	offset tdp.Offset
	number uint32 // field number; stored in the which-word for oneof members

	// jsonName and next drive schema-order prediction: JSON objects usually
	// list members in field order, so after matching this field the parser
	// compares the next key against next.jsonName before falling back to the
	// name map.
	jsonName string
	next     *dfield

	enums map[string]protoreflect.EnumNumber
	subTy *tdp.Type // message/group field type (map value type for maps)
	sub   *dplan

	key, val *dfield // map key (class only) and value

	fd protoreflect.FieldDescriptor
}

// dplan is a compiled direct-write plan for one *tdp.Type.
type dplan struct {
	ty *tdp.Type
	md protoreflect.MessageDescriptor

	wkt    uint8
	byName map[string]*dfield
	byIdx  []dfield // field-index order, used by WKT writers

	words    int
	nfields  uint32
	required bool

	// direct reports whether this type and everything reachable from it is
	// supported by the direct writer; when false, Unmarshal uses the
	// JSON-to-wire transcoder instead.
	direct bool

	wrapped *dfield // wkWrapper: the "value" field
}

var (
	dplanCache sync.Map // *tdp.Type -> *dplan
	dplanMu    sync.Mutex
)

func dplanFor(ty *tdp.Type) *dplan {
	if p, ok := dplanCache.Load(ty); ok {
		return p.(*dplan) //nolint:errcheck
	}
	dplanMu.Lock()
	defer dplanMu.Unlock()
	if p, ok := dplanCache.Load(ty); ok {
		return p.(*dplan) //nolint:errcheck
	}
	built := make(map[*tdp.Type]*dplan)
	p := buildDPlan(ty, built)

	// Propagate required and !direct through message cycles to a fixpoint
	// before publishing.
	for changed := true; changed; {
		changed = false
		for _, v := range built {
			for i := range v.byIdx {
				sub := v.byIdx[i].sub
				if sub == nil {
					continue
				}
				if sub.required && !v.required {
					v.required = true
					changed = true
				}
				if !sub.direct && v.direct {
					v.direct = false
					changed = true
				}
			}
		}
	}
	for k, v := range built {
		dplanCache.Store(k, v)
	}
	return p
}

func buildDPlan(ty *tdp.Type, built map[*tdp.Type]*dplan) *dplan {
	if p, ok := dplanCache.Load(ty); ok {
		return p.(*dplan) //nolint:errcheck
	}
	if p, ok := built[ty]; ok {
		return p
	}
	md := ty.Descriptor
	p := &dplan{ty: ty, md: md, wkt: wktCode(md.FullName()), direct: true}
	built[ty] = p

	fds := md.Fields()
	oneofs := md.Oneofs()
	p.nfields = uint32(fds.Len())
	p.words = (fds.Len() + oneofs.Len() + 63) / 64
	p.byIdx = make([]dfield, fds.Len())
	p.byName = make(map[string]*dfield, fds.Len()*2)

	for i := range fds.Len() {
		fd := fds.Get(i)
		if fd.Cardinality() == protoreflect.Required {
			p.required = true
		}
		f := ty.ByIndex(i)
		df := &p.byIdx[i]
		classifyD(df, fd, f, built)
		df.idx = uint32(i)
		df.oneof = -1
		if od := fd.ContainingOneof(); od != nil && !od.IsSynthetic() {
			df.oneof = int32(p.nfields) + int32(od.Index())
		}
		if !supportedD(df) {
			p.direct = false
		}
		df.jsonName = fd.JSONName()
		p.byName[df.jsonName] = df
		p.byName[string(fd.Name())] = df
	}
	for i := range p.byIdx {
		if i+1 < len(p.byIdx) {
			p.byIdx[i].next = &p.byIdx[i+1]
		}
	}
	if p.wkt == wkWrapper {
		p.wrapped = p.byName["value"]
	}
	return p
}

// classifyD fills in a dfield from its descriptor and compiled tdp field.
func classifyD(df *dfield, fd protoreflect.FieldDescriptor, f *tdp.Field, built map[*tdp.Type]*dplan) {
	df.fd = fd
	df.number = uint32(fd.Number())
	df.offset = f.Accessor.Offset

	switch {
	case fd.IsMap():
		df.isMap = true
		df.presence = dpPointer
		df.key = &dfield{}
		classifyDScalar(df.key, fd.MapKey(), f, built)
		df.val = &dfield{}
		classifyDScalar(df.val, fd.MapValue(), f, built)
		df.subTy = f.Message // the map value's message type, if any
		if df.val.sub != nil {
			df.val.subTy = f.Message
		}
		return
	case fd.IsList():
		df.isList = true
		df.presence = dpImplicit
	default:
		od := fd.ContainingOneof()
		switch {
		case od != nil && !od.IsSynthetic() && df.offset.Number != 0:
			// A oneof implemented as a real union. Single-member oneofs have
			// Offset.Number == 0 and degrade to the optional archetypes.
			df.presence = dpOneof
		case fd.Message() != nil:
			df.presence = dpPointer
		case fd.HasPresence():
			df.presence = dpHasbit
		default:
			df.presence = dpImplicit
		}
	}
	classifyDScalar(df, fd, f, built)
}

// classifyDScalar fills the value-class parts of a dfield (also used for map
// key/value pseudo-fields, which have no offset or presence of their own).
func classifyDScalar(df *dfield, fd protoreflect.FieldDescriptor, f *tdp.Field, built map[*tdp.Type]*dplan) {
	df.fd = fd
	df.class, _ = classifyKind(fd.Kind())
	switch fd.Kind() {
	case protoreflect.EnumKind:
		df.nullEnum, df.allowsNull = isNullEnum(fd)
		df.enums = buildEnumMap(fd.Enum())
	case protoreflect.GroupKind:
		df.subTy = f.Message
		df.sub = buildDPlan(f.Message, built)
		df.wkt = df.sub.wkt
	case protoreflect.MessageKind:
		df.subTy = f.Message
		if f.Message != nil {
			df.sub = buildDPlan(f.Message, built)
			df.wkt = df.sub.wkt
			df.allowsNull = df.wkt == wkValue && !fd.IsList() && !fd.IsMap()
		}
	}
}


// supportedD reports whether the direct writer handles this field shape.
func supportedD(df *dfield) bool {
	// Message-typed fields need a compiled subtype in the tdp tables.
	check := func(f *dfield) bool {
		if f.class == ucMessage || f.class == ucGroup {
			return f.subTy != nil && f.sub != nil
		}
		return true
	}
	if df.isMap {
		return check(df.key) && check(df.val)
	}
	return check(df)
}
