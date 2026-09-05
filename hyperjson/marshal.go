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
	"errors"
	"fmt"
	"math"
	"slices"
	"sort"
	"strings"
	"sync"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"

	"buf.build/go/hyperpb"
	"buf.build/go/hyperpb/internal/tdp/dynamic"
	"buf.build/go/hyperpb/internal/xprotoreflect"
)

// Resolver resolves google.protobuf.Any type URLs.
type Resolver interface {
	FindMessageByURL(url string) (protoreflect.MessageType, error)
}

// ExtensionResolver resolves extension field names.
type ExtensionResolver interface {
	FindExtensionByName(field protoreflect.FullName) (protoreflect.ExtensionType, error)
}

func findExtension(r Resolver, name protoreflect.FullName) (protoreflect.ExtensionType, error) {
	if er, ok := r.(ExtensionResolver); ok {
		return er.FindExtensionByName(name)
	}
	return protoregistry.GlobalTypes.FindExtensionByName(name)
}

// MarshalOptions configures Marshal.
type MarshalOptions struct {
	// Multiline specifies whether the marshaler should format the output in
	// indented-form with every textual element on a new line.
	// If Indent is an empty string, then an arbitrary indent of two spaces is chosen.
	Multiline bool

	// Indent specifies the set of indentation characters to use in a multiline
	// formatted output such that every entry is preceded by Indent and
	// terminated by a newline. If non-empty, then Multiline is treated as true.
	// Indent can only be composed of space or tab characters.
	Indent string

	// UseProtoNames emits fields under their proto (snake_case) names instead
	// of their JSON (lowerCamelCase) names.
	UseProtoNames bool

	// UseEnumNumbers emits enum values as numbers.
	UseEnumNumbers bool

	// EmitUnpopulated specifies whether to emit unpopulated fields. It does not
	// emit unpopulated oneof fields or unpopulated extension fields.
	EmitUnpopulated bool

	// EmitDefaultValues specifies whether to emit default-valued primitive fields,
	// empty lists, and empty maps.
	// EmitUnpopulated takes precedence over EmitDefaultValues.
	EmitDefaultValues bool

	// AllowPartial allows marshaling messages with missing required fields.
	AllowPartial bool

	// Resolver is used to resolve google.protobuf.Any type URLs. If nil,
	// protoregistry.GlobalTypes is used.
	Resolver Resolver
}

// Marshal serializes a protobuf message to protojson-compatible JSON.
//
// For hyperpb messages, it walks the compiled tdp tables directly.
// For standard generated proto.Message implementations, it walks the
// protoreflect surface.
func Marshal(msg proto.Message) ([]byte, error) {
	return MarshalOptions{}.Marshal(msg)
}

// MarshalAppend appends the protojson-compatible JSON serialization of msg to dst.
func MarshalAppend(dst []byte, msg proto.Message) ([]byte, error) {
	return MarshalOptions{}.MarshalAppend(dst, msg)
}

// Marshal serializes a protobuf message to protojson-compatible JSON.
func (o MarshalOptions) Marshal(msg proto.Message) ([]byte, error) {
	if msg == nil {
		return nil, errors.New("hyperjson: message is nil")
	}
	pm := msg.ProtoReflect()
	if !pm.IsValid() {
		return nil, errors.New("hyperjson: message is nil")
	}
	if !o.AllowPartial {
		if err := proto.CheckInitialized(msg); err != nil {
			return nil, err
		}
	}
	var err error
	o, err = o.normalize()
	if err != nil {
		return nil, err
	}
	m := marshalerPool.Get().(*marshaler) //nolint:errcheck
	m.opts = o
	m.e.buf = m.e.buf[:0]
	m.e.indent = ""
	m.e.depth = 0
	if o.Multiline {
		m.e.indent = o.Indent
	}
	if err := m.msgValue(pm); err != nil {
		m.opts = MarshalOptions{}
		m.e.indent = ""
		m.e.depth = 0
		if cap(m.e.buf) <= 1<<20 {
			marshalerPool.Put(m)
		}
		return nil, err
	}
	out := m.e.bytes()
	m.opts = MarshalOptions{}
	m.e.indent = ""
	m.e.depth = 0
	if cap(m.e.buf) <= 1<<20 {
		marshalerPool.Put(m)
	}
	return out, nil
}

// MarshalAppend appends the protojson-compatible JSON serialization of msg to dst.
func (o MarshalOptions) MarshalAppend(dst []byte, msg proto.Message) ([]byte, error) {
	if msg == nil {
		return nil, errors.New("hyperjson: message is nil")
	}
	pm := msg.ProtoReflect()
	if !pm.IsValid() {
		return nil, errors.New("hyperjson: message is nil")
	}
	if !o.AllowPartial {
		if err := proto.CheckInitialized(msg); err != nil {
			return nil, err
		}
	}
	var err error
	o, err = o.normalize()
	if err != nil {
		return nil, err
	}
	m := marshalerPool.Get().(*marshaler) //nolint:errcheck
	m.opts = o
	m.e.buf = m.e.buf[:0]
	m.e.indent = ""
	m.e.depth = 0
	if o.Multiline {
		m.e.indent = o.Indent
	}
	if err := m.msgValue(pm); err != nil {
		m.opts = MarshalOptions{}
		m.e.indent = ""
		m.e.depth = 0
		if cap(m.e.buf) <= 1<<20 {
			marshalerPool.Put(m)
		}
		return nil, err
	}
	dst = append(dst, m.e.buf...)
	m.opts = MarshalOptions{}
	m.e.indent = ""
	m.e.depth = 0
	if cap(m.e.buf) <= 1<<20 {
		marshalerPool.Put(m)
	}
	return dst, nil
}

func (o MarshalOptions) normalize() (MarshalOptions, error) {
	if o.Indent != "" {
		for _, c := range o.Indent {
			if c != ' ' && c != '\t' {
				return o, fmt.Errorf("hyperjson: indent contains invalid character %q", c)
			}
		}
		o.Multiline = true
	} else if o.Multiline {
		o.Indent = "  "
	}
	if o.EmitUnpopulated {
		o.EmitDefaultValues = false
	}
	return o, nil
}

// unwrapMessage converts the public message wrapper to its internal
// dynamic.Message representation.
func unwrapMessage(m *hyperpb.Message) *dynamic.Message {
	return m.Unwrap()
}

type marshaler struct {
	e     *encoder
	opts  MarshalOptions
	depth int
}

var marshalerPool = sync.Pool{
	New: func() any {
		return &marshaler{
			e: &encoder{buf: make([]byte, 0, 1024)},
		}
	},
}

// msgValue marshals any message value, dispatching between the compiled-plan
// fast path for hyperpb messages, the custom well-known-type shapes, and a
// generic protoreflect walk for foreign message implementations.
func (m *marshaler) msgValue(pm protoreflect.Message) error {
	m.depth++
	if m.depth > maxDepth {
		return errors.New("hyperjson: exceeded max recursion depth")
	}
	defer func() { m.depth-- }()

	if hm, ok := pm.(*hyperpb.Message); ok {
		return m.fastMessage(unwrapMessage(hm))
	}
	return m.overlayMessage(pm)
}

// overlayMessage marshals through the protoreflect surface, which is
// overlay-aware for mutated hyperpb messages and also handles foreign
// message implementations (e.g. values stored into an overlay).
func (m *marshaler) overlayMessage(pm protoreflect.Message) error {
	md := pm.Descriptor()
	if isCustomWKT(md.FullName()) {
		return m.wkt(pm, md)
	}
	m.e.openObject()
	first, err := m.genericFields(pm, true)
	if err != nil {
		return err
	}
	m.e.closeObject(first)
	return nil
}

// genericFields writes fields using protoreflect operations.
func (m *marshaler) genericFields(pm protoreflect.Message, first bool) (bool, error) {
	fds := pm.Descriptor().Fields()
	for i := range fds.Len() {
		fd := fds.Get(i)
		if pm.Has(fd) {
			first = m.e.memberKey(m.fieldName(fd), first)
			if err := m.value(fd, pm.Get(fd)); err != nil {
				return first, err
			}
			continue
		}

		if fd.ContainingOneof() != nil || fd.IsExtension() {
			continue // ignore unpopulated oneofs and extensions
		}

		switch {
		case m.opts.EmitUnpopulated:
			switch {
			case fd.HasPresence():
				first = m.e.memberKey(m.fieldName(fd), first)
				m.e.null()
			case fd.IsList():
				first = m.e.memberKey(m.fieldName(fd), first)
				m.e.openArray()
				m.e.closeArray(true)
			case fd.IsMap():
				first = m.e.memberKey(m.fieldName(fd), first)
				m.e.openObject()
				m.e.closeObject(true)
			default:
				first = m.e.memberKey(m.fieldName(fd), first)
				if err := m.value(fd, pm.Get(fd)); err != nil {
					return first, err
				}
			}

		case m.opts.EmitDefaultValues:
			if fd.HasPresence() {
				continue
			}
			switch {
			case fd.IsList():
				first = m.e.memberKey(m.fieldName(fd), first)
				m.e.openArray()
				m.e.closeArray(true)
			case fd.IsMap():
				first = m.e.memberKey(m.fieldName(fd), first)
				m.e.openObject()
				m.e.closeObject(true)
			default:
				first = m.e.memberKey(m.fieldName(fd), first)
				if err := m.value(fd, pm.Get(fd)); err != nil {
					return first, err
				}
			}
		}
	}
	return first, nil
}

func (m *marshaler) fieldName(fd protoreflect.FieldDescriptor) string {
	if fd.IsExtension() {
		return "[" + string(fd.FullName()) + "]"
	}
	if m.opts.UseProtoNames {
		return string(fd.Name())
	}
	return fd.JSONName()
}

// value marshals a field value: list, map, or singular.
func (m *marshaler) value(fd protoreflect.FieldDescriptor, v protoreflect.Value) error {
	switch {
	case fd.IsMap():
		return m.mapValue(fd, xprotoreflect.Map(v))
	case fd.IsList():
		list := xprotoreflect.List(v)
		n := list.Len()
		m.e.openArray()
		for i := range n {
			m.e.arrayElem(i == 0)
			if err := m.singular(fd, list.Get(i)); err != nil {
				return err
			}
		}
		m.e.closeArray(n == 0)
		return nil
	default:
		return m.singular(fd, v)
	}
}

// singular marshals a non-repeated value of the field's kind.
func (m *marshaler) singular(fd protoreflect.FieldDescriptor, v protoreflect.Value) error {
	if !v.IsValid() {
		m.e.null()
		return nil
	}
	switch fd.Kind() {
	case protoreflect.BoolKind:
		m.e.boolean(v.Bool())
	case protoreflect.Int32Kind, protoreflect.Sint32Kind, protoreflect.Sfixed32Kind:
		m.e.int32(int32(v.Int()))
	case protoreflect.Int64Kind, protoreflect.Sint64Kind, protoreflect.Sfixed64Kind:
		m.e.int64(v.Int())
	case protoreflect.Uint32Kind, protoreflect.Fixed32Kind:
		m.e.uint32(uint32(v.Uint()))
	case protoreflect.Uint64Kind, protoreflect.Fixed64Kind:
		m.e.uint64(v.Uint())
	case protoreflect.FloatKind:
		m.e.float(v.Float(), 32)
	case protoreflect.DoubleKind:
		m.e.float(v.Float(), 64)
	case protoreflect.StringKind:
		m.e.str(xprotoreflect.GetString(v))
	case protoreflect.BytesKind:
		m.e.base64(v.Bytes())
	case protoreflect.EnumKind:
		m.enum(fd, v.Enum())
	case protoreflect.MessageKind, protoreflect.GroupKind:
		return m.msgValue(xprotoreflect.GetMessage[protoreflect.Message](v))
	default:
		return fmt.Errorf("hyperjson: unsupported kind %v", fd.Kind())
	}
	return nil
}

func (m *marshaler) enum(fd protoreflect.FieldDescriptor, n protoreflect.EnumNumber) {
	ed := fd.Enum()
	if ed.FullName() == wktNullValue {
		m.e.raw("null")
		return
	}
	if !m.opts.UseEnumNumbers {
		if vd := ed.Values().ByNumber(n); vd != nil {
			m.e.str(string(vd.Name()))
			return
		}
	}
	m.e.int32(int32(n))
}

// mapValue marshals a map field with deterministically sorted keys, matching
// protojson's ordering (bools, then numeric, then lexicographic strings).
func (m *marshaler) mapValue(fd protoreflect.FieldDescriptor, mp protoreflect.Map) error {
	type entry struct {
		k protoreflect.MapKey
		v protoreflect.Value
	}
	entries := make([]entry, 0, mp.Len())
	mp.Range(func(k protoreflect.MapKey, v protoreflect.Value) bool {
		entries = append(entries, entry{k, v})
		return true
	})

	keyKind := fd.MapKey().Kind()
	sort.Slice(entries, func(i, j int) bool {
		a, b := entries[i].k, entries[j].k
		switch keyKind {
		case protoreflect.BoolKind:
			return !a.Bool() && b.Bool()
		case protoreflect.Int32Kind, protoreflect.Sint32Kind, protoreflect.Sfixed32Kind,
			protoreflect.Int64Kind, protoreflect.Sint64Kind, protoreflect.Sfixed64Kind:
			return a.Int() < b.Int()
		case protoreflect.Uint32Kind, protoreflect.Fixed32Kind,
			protoreflect.Uint64Kind, protoreflect.Fixed64Kind:
			return a.Uint() < b.Uint()
		default:
			return a.String() < b.String()
		}
	})

	m.e.openObject()
	valueFd := fd.MapValue()
	for i, ent := range entries {
		m.writeMapKey(keyKind, ent.k, i == 0)
		if err := m.singular(valueFd, ent.v); err != nil {
			return err
		}
	}
	m.e.closeObject(len(entries) == 0)
	return nil
}

func (m *marshaler) writeMapKey(kind protoreflect.Kind, k protoreflect.MapKey, first bool) {
	if len(m.e.indent) == 0 {
		if !first {
			m.e.rawByte(',')
		}
		m.mapKey(kind, k)
		m.e.rawByte(':')
		return
	}
	if !first {
		m.e.rawByte(',')
	}
	m.e.rawByte('\n')
	m.e.writeIndent()
	m.mapKey(kind, k)
	m.e.raw(":  ")
}

// mapKey writes a map key as a JSON string.
func (m *marshaler) mapKey(kind protoreflect.Kind, k protoreflect.MapKey) {
	switch kind {
	case protoreflect.BoolKind:
		if k.Bool() {
			m.e.raw(`"true"`)
		} else {
			m.e.raw(`"false"`)
		}
	case protoreflect.Int32Kind, protoreflect.Sint32Kind, protoreflect.Sfixed32Kind,
		protoreflect.Int64Kind, protoreflect.Sint64Kind, protoreflect.Sfixed64Kind:
		m.e.int64(k.Int())
	case protoreflect.Uint32Kind, protoreflect.Fixed32Kind,
		protoreflect.Uint64Kind, protoreflect.Fixed64Kind:
		m.e.uint64(k.Uint())
	default:
		m.e.str(k.String())
	}
}

// wkt marshals the custom shapes of the well-known types.
func (m *marshaler) wkt(pm protoreflect.Message, md protoreflect.MessageDescriptor) error {
	fds := md.Fields()
	switch string(md.FullName()) {
	case wktTimestamp:
		seconds := pm.Get(fds.ByNumber(1)).Int()
		nanos := int32(pm.Get(fds.ByNumber(2)).Int())
		buf, err := appendTimestamp(m.e.buf, seconds, nanos)
		if err != nil {
			return err
		}
		m.e.buf = buf
		return nil

	case wktDuration:
		seconds := pm.Get(fds.ByNumber(1)).Int()
		nanos := int32(pm.Get(fds.ByNumber(2)).Int())
		buf, err := appendDuration(m.e.buf, seconds, nanos)
		if err != nil {
			return err
		}
		m.e.buf = buf
		return nil

	case wktEmpty:
		m.e.raw("{}")
		return nil

	case wktStruct:
		return m.structValue(pm, fds)

	case wktValue:
		return m.valueValue(pm, fds)

	case wktListValue:
		list := pm.Get(fds.ByNumber(1)).List()
		n := list.Len()
		m.e.openArray()
		for i := range n {
			m.e.arrayElem(i == 0)
			inner := xprotoreflect.GetMessage[protoreflect.Message](list.Get(i))
			if err := m.wkt(inner, inner.Descriptor()); err != nil {
				return err
			}
		}
		m.e.closeArray(n == 0)
		return nil

	case wktFieldMask:
		return m.fieldMask(pm, fds)

	case wktAny:
		return m.anyValue(pm, fds)

	default:
		// Wrapper types: single "value" field with the scalar's normal shape.
		return m.singular(fds.ByNumber(1), pm.Get(fds.ByNumber(1)))
	}
}

func (m *marshaler) structValue(pm protoreflect.Message, fds protoreflect.FieldDescriptors) error {
	fd := fds.ByNumber(1)
	mp := pm.Get(fd).Map()

	type entry struct {
		k string
		v protoreflect.Value
	}
	entries := make([]entry, 0, mp.Len())
	mp.Range(func(k protoreflect.MapKey, v protoreflect.Value) bool {
		entries = append(entries, entry{k.String(), v})
		return true
	})
	slices.SortFunc(entries, func(a, b entry) int { return strings.Compare(a.k, b.k) })

	m.e.openObject()
	for i, ent := range entries {
		m.e.memberKey(ent.k, i == 0)
		inner := xprotoreflect.GetMessage[protoreflect.Message](ent.v)
		if err := m.wkt(inner, inner.Descriptor()); err != nil {
			return err
		}
	}
	m.e.closeObject(len(entries) == 0)
	return nil
}

func (m *marshaler) valueValue(pm protoreflect.Message, fds protoreflect.FieldDescriptors) error {
	var found bool
	var err error
	pm.Range(func(fd protoreflect.FieldDescriptor, v protoreflect.Value) bool {
		found = true
		switch fd.Number() {
		case 1: // null_value
			m.e.raw("null")
		case 2: // number_value
			f := v.Float()
			// protojson rejects non-finite number_value.
			if math.IsNaN(f) || math.IsInf(f, 0) {
				err = errors.New("hyperjson: google.protobuf.Value number_value is not finite")
				return false
			}
			m.e.float(f, 64)
		case 3: // string_value
			m.e.str(xprotoreflect.GetString(v))
		case 4: // bool_value
			m.e.boolean(v.Bool())
		case 5, 6: // struct_value, list_value
			inner := xprotoreflect.GetMessage[protoreflect.Message](v)
			err = m.wkt(inner, inner.Descriptor())
		}
		return false
	})
	if err != nil {
		return err
	}
	if !found {
		// An unpopulated Value has no default JSON representation.
		return errors.New("hyperjson: google.protobuf.Value has no value set")
	}
	return nil
}

func (m *marshaler) fieldMask(pm protoreflect.Message, fds protoreflect.FieldDescriptors) error {
	list := pm.Get(fds.ByNumber(1)).List()
	m.e.rawByte('"')
	for i := range list.Len() {
		if i > 0 {
			m.e.rawByte(',')
		}
		path := list.Get(i).String()
		cc := jsonCamelCase(path)
		if path != jsonSnakeCase(cc) {
			return fmt.Errorf("hyperjson: irreversible google.protobuf.FieldMask path %q", path)
		}
		m.e.raw(cc)
	}
	m.e.rawByte('"')
	return nil
}

func (m *marshaler) anyValue(pm protoreflect.Message, fds protoreflect.FieldDescriptors) error {
	typeURL := pm.Get(fds.ByNumber(1)).String()
	value := pm.Get(fds.ByNumber(2)).Bytes()
	if typeURL == "" && len(value) == 0 {
		m.e.openObject()
		m.e.closeObject(true)
		return nil
	}

	inner, err := m.parseAny(typeURL, value)
	if err != nil {
		return err
	}

	innerMd := inner.Descriptor()
	m.e.openObject()
	m.e.memberKey("@type", true)
	m.e.str(typeURL)
	switch {
	case innerMd.FullName() == wktEmpty:
		// google.protobuf.Empty's JSON form is {}; protojson omits the
		// "value" member entirely.
	case isCustomWKT(innerMd.FullName()):
		m.e.memberKey("value", false)
		if err := m.wkt(inner, innerMd); err != nil {
			return err
		}
	default:
		dm := unwrapMessage(inner)
		if _, err := m.genericFields(dm.ProtoReflect(), false); err != nil {
			return err
		}
	}
	m.e.closeObject(false)
	return nil
}

// typeCache caches hyperpb compilations of Any payload types.
var typeCache sync.Map // protoreflect.MessageDescriptor -> *hyperpb.MessageType

func compiledType(md protoreflect.MessageDescriptor) *hyperpb.MessageType {
	if t, ok := typeCache.Load(md); ok {
		return t.(*hyperpb.MessageType) //nolint:errcheck
	}
	t := hyperpb.CompileMessageDescriptor(md)
	actual, _ := typeCache.LoadOrStore(md, t)
	return actual.(*hyperpb.MessageType) //nolint:errcheck
}

// parseAny resolves an Any type URL and parses its payload with hyperpb.
func (m *marshaler) parseAny(typeURL string, value []byte) (*hyperpb.Message, error) {
	resolver := m.opts.Resolver
	if resolver == nil {
		resolver = protoregistry.GlobalTypes
	}
	mt, err := resolver.FindMessageByURL(typeURL)
	if err != nil {
		return nil, fmt.Errorf("hyperjson: cannot resolve google.protobuf.Any type URL %q: %w", typeURL, err)
	}
	ct := compiledType(mt.Descriptor())
	msg := hyperpb.NewMessage(ct)
	if err := msg.Unmarshal(value); err != nil {
		return nil, fmt.Errorf("hyperjson: cannot parse google.protobuf.Any payload for %q: %w", typeURL, err)
	}
	return msg, nil
}
