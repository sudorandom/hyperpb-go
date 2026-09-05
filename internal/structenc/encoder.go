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
	"errors"
	"fmt"
	"reflect"
	"sync"
	"unsafe"

	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"

	"buf.build/go/hyperpb/internal/xunsafe"
)

type fieldEntry struct {
	num      protowire.Number
	tagBytes [10]byte
	tagLen   uint8
	offset   uintptr
	encode   encodeFunc
	subEnc   *Encoder
	mapEnc   *mapFieldEncoder
	oneofEnc *oneofFieldEncoder
	isGroup  bool
}

type reqFieldInfo struct {
	offset uintptr
	name   string
}

// Encoder encodes a Go struct directly into protobuf wire format.
type Encoder struct {
	structType    reflect.Type
	fields        []fieldEntry
	reqFields     []reqFieldInfo
	hasUnknown    bool
	unknownOffset uintptr
}

type encodeFunc func(b []byte, base unsafe.Pointer, f *fieldEntry, opts Options) ([]byte, error)

var (
	encoderCache sync.Map // map[reflect.Type]*Encoder
	compileMu    sync.Mutex
)

// Get returns a cached Encoder for the given proto.Message struct, compiling it if necessary.
func Get(msg proto.Message) (*Encoder, error) {
	if msg == nil {
		return nil, ErrNilMessage
	}
	t := reflect.TypeOf(msg)
	if t.Kind() != reflect.Pointer || t.Elem().Kind() != reflect.Struct {
		return nil, fmt.Errorf("structenc: expected pointer to struct, got %v", t)
	}
	structType := t.Elem()
	if v, ok := encoderCache.Load(structType); ok {
		if enc, ok := v.(*Encoder); ok {
			return enc, nil
		}
	}
	return CompileType(structType)
}

// Compile compiles an Encoder for message type T.
func Compile[T proto.Message]() (*Encoder, error) {
	var zero T
	t := reflect.TypeOf(zero)
	if t == nil {
		t = reflect.TypeFor[T]()
	}
	if t.Kind() != reflect.Pointer || t.Elem().Kind() != reflect.Struct {
		return nil, fmt.Errorf("structenc: expected pointer to struct, got %v", t)
	}
	structType := t.Elem()
	if v, ok := encoderCache.Load(structType); ok {
		if enc, ok := v.(*Encoder); ok {
			return enc, nil
		}
	}
	return CompileType(structType)
}

// CompileType compiles an Encoder for the given struct type.
func CompileType(structType reflect.Type) (*Encoder, error) {
	compileMu.Lock()
	defer compileMu.Unlock()

	if v, ok := encoderCache.Load(structType); ok {
		if enc, ok := v.(*Encoder); ok {
			return enc, nil
		}
	}
	visited := make(map[reflect.Type]*Encoder)
	enc, err := compileStruct(structType, visited)
	if err != nil {
		return nil, err
	}
	for typ, d := range visited {
		encoderCache.Store(typ, d)
	}
	return enc, nil
}

// EncodeMessage serializes msg directly into protobuf wire format.
func (e *Encoder) EncodeMessage(msg proto.Message, b []byte, opts Options) ([]byte, error) {
	if msg == nil {
		return b, nil
	}
	base := unsafe.Pointer(xunsafe.AnyData(msg))
	if base == nil {
		return b, nil
	}
	return e.Encode(base, b, opts)
}

// Encode serializes the struct located at base into b.
func (e *Encoder) Encode(base unsafe.Pointer, b []byte, opts Options) ([]byte, error) {
	if base == nil {
		return b, nil
	}

	// 1. Check required fields if !AllowPartial
	if !opts.AllowPartial && len(e.reqFields) > 0 {
		for i := range e.reqFields {
			rf := &e.reqFields[i]
			p := *(*unsafe.Pointer)(unsafe.Add(base, rf.offset))
			if p == nil {
				return nil, errors.Join(ErrRequiredNotSet, fmt.Errorf("field %s is not set", rf.name))
			}
		}
	}

	// 2. Encode all fields in ascending tag order
	for i := range e.fields {
		f := &e.fields[i]
		var err error
		b, err = f.encode(b, base, f, opts)
		if err != nil {
			return nil, err
		}
	}

	// 3. Append unknown fields
	if e.hasUnknown {
		unknown := *(*[]byte)(unsafe.Add(base, e.unknownOffset))
		if len(unknown) > 0 {
			b = append(b, unknown...)
		}
	}

	return b, nil
}
