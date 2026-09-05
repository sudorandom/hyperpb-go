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
	"slices"

	"google.golang.org/protobuf/reflect/protoreflect"

	"buf.build/go/hyperpb/internal/tdp"
)

// CompileHook is initialized by the hyperpb package to avoid circular dependencies.
var CompileHook func(protoreflect.MessageDescriptor) *tdp.Type

func resolveType(shared *Shared, explicitType *tdp.Type, md protoreflect.MessageDescriptor) *tdp.Type {
	if explicitType != nil {
		return explicitType
	}
	if shared != nil && shared.Library() != nil {
		if t, ok := shared.Library().Type(md); ok {
			return t
		}
	}
	if CompileHook != nil {
		return CompileHook(md)
	}
	panic("hyperpb: compile hook not set")
}

func zeroValueForKind(k protoreflect.Kind) protoreflect.Value {
	switch k {
	case protoreflect.BoolKind:
		return protoreflect.ValueOfBool(false)
	case protoreflect.Int32Kind, protoreflect.Sint32Kind, protoreflect.Sfixed32Kind:
		return protoreflect.ValueOfInt32(0)
	case protoreflect.Int64Kind, protoreflect.Sint64Kind, protoreflect.Sfixed64Kind:
		return protoreflect.ValueOfInt64(0)
	case protoreflect.Uint32Kind, protoreflect.Fixed32Kind:
		return protoreflect.ValueOfUint32(0)
	case protoreflect.Uint64Kind, protoreflect.Fixed64Kind:
		return protoreflect.ValueOfUint64(0)
	case protoreflect.FloatKind:
		return protoreflect.ValueOfFloat32(0)
	case protoreflect.DoubleKind:
		return protoreflect.ValueOfFloat64(0)
	case protoreflect.StringKind:
		return protoreflect.ValueOf("")
	case protoreflect.BytesKind:
		return protoreflect.ValueOfBytes(nil)
	case protoreflect.EnumKind:
		return protoreflect.ValueOfEnum(0)
	default:
		return protoreflect.Value{}
	}
}

func isZeroValue(fd protoreflect.FieldDescriptor, val protoreflect.Value) bool {
	if !val.IsValid() {
		return true
	}
	switch fd.Kind() {
	case protoreflect.BoolKind:
		return !val.Bool()
	case protoreflect.Int32Kind, protoreflect.Sint32Kind, protoreflect.Sfixed32Kind,
		protoreflect.Int64Kind, protoreflect.Sint64Kind, protoreflect.Sfixed64Kind:
		return val.Int() == 0
	case protoreflect.Uint32Kind, protoreflect.Fixed32Kind,
		protoreflect.Uint64Kind, protoreflect.Fixed64Kind:
		return val.Uint() == 0
	case protoreflect.FloatKind, protoreflect.DoubleKind:
		return val.Float() == 0
	case protoreflect.StringKind:
		return len(val.String()) == 0
	case protoreflect.BytesKind:
		return len(val.Bytes()) == 0
	case protoreflect.EnumKind:
		return val.Enum() == 0
	default:
		return false
	}
}

type overlayVal struct {
	fd  protoreflect.FieldDescriptor
	val protoreflect.Value
}

type fieldSet struct {
	buf   [16]protoreflect.FieldNumber
	slice []protoreflect.FieldNumber
	set   map[protoreflect.FieldNumber]bool
}

func (s *fieldSet) init() {
	s.slice = s.buf[:0]
}

func (s *fieldSet) has(num protoreflect.FieldNumber) bool {
	if s.set != nil {
		return s.set[num]
	}
	return slices.Contains(s.slice, num)
}

func (s *fieldSet) add(num protoreflect.FieldNumber) {
	if s.set != nil {
		s.set[num] = true
		return
	}
	if len(s.slice) < len(s.buf) {
		s.slice = append(s.slice, num)
		return
	}
	s.set = make(map[protoreflect.FieldNumber]bool, len(s.buf)+1)
	for _, n := range s.slice {
		s.set[n] = true
	}
	s.set[num] = true
}

func newMessageValue(shared *Shared, explicitType *tdp.Type, md protoreflect.MessageDescriptor) protoreflect.Value {
	ty := resolveType(shared, explicitType, md)
	newMsg := shared.New(ty)
	return protoreflect.ValueOfMessage(newMsg.ProtoReflect())
}

// MessageOverlay stores the mutated fields, cleared fields, and fallback message for a Message.
type MessageOverlay struct {
	Fields   map[protoreflect.FieldNumber]overlayVal
	Cleared  map[protoreflect.FieldNumber]bool
	Fallback *Message
	Unknown  protoreflect.RawFields
}

// cowList wraps a protoreflect.List to support copy-on-write mutations.
type cowList struct {
	fd      protoreflect.FieldDescriptor
	shared  *Shared
	subType *tdp.Type
	elems   []protoreflect.Value
}

var _ protoreflect.List = (*cowList)(nil)

func newCowList(fallback protoreflect.List, fd protoreflect.FieldDescriptor, shared *Shared, subType *tdp.Type) *cowList {
	l := &cowList{
		fd:      fd,
		shared:  shared,
		subType: subType,
	}
	if fallback != nil {
		l.elems = make([]protoreflect.Value, fallback.Len())
		for i := range fallback.Len() {
			l.elems[i] = fallback.Get(i)
		}
	}
	return l
}

func (l *cowList) Len() int {
	return len(l.elems)
}

func (l *cowList) IsValid() bool {
	return true
}

func (l *cowList) Get(i int) protoreflect.Value {
	return l.elems[i]
}

func (l *cowList) Set(i int, value protoreflect.Value) {
	l.elems[i] = value
}

func (l *cowList) Append(value protoreflect.Value) {
	l.elems = append(l.elems, value)
}

func (l *cowList) AppendMutable() protoreflect.Value {
	if l.fd.Message() == nil {
		panic("AppendMutable called on non-message list")
	}
	val := newMessageValue(l.shared, l.subType, l.fd.Message())
	l.elems = append(l.elems, val)
	return val
}

func (l *cowList) Truncate(n int) {
	clear(l.elems[n:])
	l.elems = l.elems[:n]
}

func (l *cowList) NewElement() protoreflect.Value {
	if l.fd.Message() != nil {
		return newMessageValue(l.shared, l.subType, l.fd.Message())
	}
	return zeroValueForKind(l.fd.Kind())
}

// cowMap wraps a protoreflect.Map to support copy-on-write mutations.
type cowMap struct {
	fd      protoreflect.FieldDescriptor
	shared  *Shared
	valType *tdp.Type
	m       map[any]protoreflect.Value
}

var _ protoreflect.Map = (*cowMap)(nil)

func newCowMap(fallback protoreflect.Map, fd protoreflect.FieldDescriptor, shared *Shared, valType *tdp.Type) *cowMap {
	mp := &cowMap{
		fd:      fd,
		shared:  shared,
		valType: valType,
		m:       make(map[any]protoreflect.Value),
	}
	if fallback != nil {
		fallback.Range(func(key protoreflect.MapKey, val protoreflect.Value) bool {
			mp.m[key.Interface()] = val
			return true
		})
	}
	return mp
}

func (mp *cowMap) Len() int {
	return len(mp.m)
}

func (mp *cowMap) IsValid() bool {
	return true
}

func (mp *cowMap) Has(key protoreflect.MapKey) bool {
	_, ok := mp.m[key.Interface()]
	return ok
}

func (mp *cowMap) Get(key protoreflect.MapKey) protoreflect.Value {
	return mp.m[key.Interface()]
}

func (mp *cowMap) Set(key protoreflect.MapKey, value protoreflect.Value) {
	mp.m[key.Interface()] = value
}

func (mp *cowMap) Clear(key protoreflect.MapKey) {
	delete(mp.m, key.Interface())
}

func (mp *cowMap) Range(f func(protoreflect.MapKey, protoreflect.Value) bool) {
	for k, v := range mp.m {
		if !f(protoreflect.ValueOf(k).MapKey(), v) {
			return
		}
	}
}

func (mp *cowMap) Mutable(key protoreflect.MapKey) protoreflect.Value {
	if mp.fd.MapValue().Message() == nil {
		panic("Mutable called on non-message map value")
	}
	if val, ok := mp.m[key.Interface()]; ok {
		return val
	}
	val := newMessageValue(mp.shared, mp.valType, mp.fd.MapValue().Message())
	mp.m[key.Interface()] = val
	return val
}

func (mp *cowMap) NewValue() protoreflect.Value {
	if mp.fd.MapValue().Message() != nil {
		return newMessageValue(mp.shared, mp.valType, mp.fd.MapValue().Message())
	}
	return zeroValueForKind(mp.fd.MapValue().Kind())
}
