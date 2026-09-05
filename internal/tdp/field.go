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

package tdp

import (
	"fmt"
	"unsafe"

	"google.golang.org/protobuf/reflect/protoreflect"

	"buf.build/go/hyperpb/internal/debug"
	"buf.build/go/hyperpb/internal/xunsafe"
)

// Field is an optimized descriptor for a message field.
type Field struct {
	_ xunsafe.NoCopy

	// The Message type for this field, if there is one.
	Message *Type
	Accessor
}

// IsValid returns whether or not this is a valid field (non-nil and non-sentinel)
// in a [Type]'s field table.
func (f *Field) IsValid() bool {
	return f != nil && f.Getter != nil
}

// Get gets the value of this field out of a message of appropriate type.
// Returns nil if the field is unset.
//
// This performs no type-checking! Callers are responsible for ensuring that m
// is of the correct type.
func (f *Field) Get(m unsafe.Pointer) protoreflect.Value {
	if !f.IsValid() {
		return protoreflect.ValueOf(nil)
	}
	return f.Getter(m, f.Message, &f.Accessor)
}

// Format implements [fmt.Formatter].
func (f *Field) Format(s fmt.State, verb rune) {
	debug.Dict("", "message", f.Message, "getter", &f.Getter).Format(s, verb)
}

// Accessor is all the information necessary for accessing a field of a [message].
type Accessor struct {
	Offset Offset

	// The Getter for extracting the field.
	Getter Getter
}

// Format implements [fmt.Formatter].
func (a *Accessor) Format(s fmt.State, verb rune) {
	debug.Dict("", "offset", a.Offset, "getter", debug.Func(a.Getter)).Format(s, verb)
}

// Getter is a thunk for extracting a value out of a field.
type Getter func(unsafe.Pointer, *Type, *Accessor) protoreflect.Value

// FieldParser is a parser for a single field.
type FieldParser struct {
	_ xunsafe.NoCopy

	// The expected, partially decoded Tag value for the field.
	Tag Tag

	// Byte offset to the typeParser this fieldParser uses, if any.
	Message *TypeParser

	// Field Offset information for the field this parser parses. Duplicated
	// from [getter].
	Offset Offset

	// For non-singular fields, the default size to preallocate for this field.
	Preload uint32

	// The parser to jump to after this one, depending on whether the parse
	// succeeds or fails.
	NextOk, NextErr xunsafe.Addr[FieldParser]

	// The thunk to call for this field. The type of this thunk is stored in
	// the parser package.
	Parse uintptr
}

// Format implements [fmt.Formatter].
func (p *FieldParser) Format(s fmt.State, verb rune) {
	debug.Dict(
		debug.Fprintf("%p", p),
		"tag", p.Tag,
		"message", func() any {
			if p.Message == nil {
				return nil
			}
			return p.Message.TypeOffset
		}(),
		"offset", p.Offset,
		"next", func() any {
			if p.NextOk == p.NextErr {
				return debug.Fprintf("%v", p.NextOk)
			}
			return debug.Fprintf("%v/%v", p.NextOk, p.NextErr)
		}(),
		"thunk", debug.Func(p.Parse),
	).Format(s, verb)
}

// FieldLayout is layout information for a [field]. Only for debugging.
type FieldLayout struct {
	Size, Align, Bits, Padding uint32

	Index  int // Which field is this in the MessageDescriptor?
	Offset Offset
}
