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

package hyperpb

import (
	"google.golang.org/protobuf/proto"

	"buf.build/go/hyperpb/internal/structenc"
)

// Marshal returns the wire-format encoding of msg.
func Marshal(msg proto.Message, opts ...MarshalOption) ([]byte, error) {
	return MarshalAppend(nil, msg, opts...)
}

// MarshalAppend appends the wire-format encoding of msg to b.
func MarshalAppend(b []byte, msg proto.Message, opts ...MarshalOption) ([]byte, error) {
	if msg == nil {
		return b, nil
	}
	if m, ok := msg.(*Message); ok {
		if m == nil {
			return b, nil
		}
		return m.impl.MarshalMessage(b)
	}
	enc, err := structenc.Get(msg)
	if err != nil {
		stOpts := toStructencOptions(opts...)
		pOpts := proto.MarshalOptions{
			Deterministic: stOpts.Deterministic,
			AllowPartial:  stOpts.AllowPartial,
		}
		return pOpts.MarshalAppend(b, msg)
	}
	return enc.EncodeMessage(msg, b, toStructencOptions(opts...))
}

// StructEncoder is a pre-compiled, fast wire encoder for a specific struct type.
type StructEncoder[T proto.Message] struct {
	enc *structenc.Encoder
}

// CompileMarshal compiles a high-performance StructEncoder for the proto.Message struct type T.
func CompileMarshal[T proto.Message]() (*StructEncoder[T], error) {
	enc, err := structenc.Compile[T]()
	if err != nil {
		return nil, err
	}
	return &StructEncoder[T]{enc: enc}, nil
}

// Marshal serializes msg into wire format.
func (e *StructEncoder[T]) Marshal(msg T, opts ...MarshalOption) ([]byte, error) {
	return e.MarshalAppend(nil, msg, opts...)
}

// MarshalAppend serializes msg into b.
func (e *StructEncoder[T]) MarshalAppend(b []byte, msg T, opts ...MarshalOption) ([]byte, error) {
	return e.enc.EncodeMessage(msg, b, toStructencOptions(opts...))
}
