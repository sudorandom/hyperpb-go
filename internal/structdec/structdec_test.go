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

package structdec_test

import (
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	"buf.build/go/hyperpb/internal/gen/conformance"
	testpb "buf.build/go/hyperpb/internal/gen/test"
	"buf.build/go/hyperpb/internal/structdec"
)

func TestDecodeScalars(t *testing.T) {
	t.Parallel()

	b1 := int32(100)
	b14 := "hello optional"
	orig := &testpb.Scalars{
		A1:  1,
		A2:  2,
		A3:  3,
		A4:  4,
		A5:  -5,
		A6:  -6,
		A7:  7,
		A8:  8,
		A9:  -9,
		A10: -10,
		A11: 11.5,
		A12: 12.5,
		A13: true,
		A14: "scalar string",
		A15: []byte("scalar bytes"),
		B1:  &b1,
		B14: &b14,
	}

	wire, err := proto.Marshal(orig)
	require.NoError(t, err)

	dec, err := structdec.Get(orig)
	require.NoError(t, err)

	got := &testpb.Scalars{}
	err = dec.DecodeMessage(got, wire, structdec.DefaultOptions())
	require.NoError(t, err)

	assert.True(t, proto.Equal(orig, got), "expected Equal:\n  orig: %v\n  got:  %v", orig, got)
}

func TestDecodeRepeated(t *testing.T) {
	t.Parallel()

	orig := &testpb.Repeated{
		R1: []int32{1, 2, 3},
		R2: []int64{10, 20, 30},
		R3: []int32{-1, -2, -3},
		R4: []int64{-10, -20, -30},
		R5: []uint32{100, 200},
		R6: []uint64{1000, 2000},
		R7: []string{"foo", "bar"},
		R8: [][]byte{[]byte("bin1"), []byte("bin2")},
	}

	wire, err := proto.Marshal(orig)
	require.NoError(t, err)

	dec, err := structdec.Get(orig)
	require.NoError(t, err)

	got := &testpb.Repeated{}
	err = dec.DecodeMessage(got, wire, structdec.DefaultOptions())
	require.NoError(t, err)

	assert.True(t, proto.Equal(orig, got), "expected Equal:\n  orig: %v\n  got:  %v", orig, got)
}

func TestDecodeGraph(t *testing.T) {
	t.Parallel()

	orig := &testpb.Graph{
		V: 1,
		S: &testpb.Graph{
			V: 2,
			S: &testpb.Graph{
				V: 3,
			},
		},
		R: []*testpb.Graph{
			{V: 10},
			{V: 20, S: &testpb.Graph{V: 21}},
		},
	}

	wire, err := proto.Marshal(orig)
	require.NoError(t, err)

	dec, err := structdec.Get(orig)
	require.NoError(t, err)

	got := &testpb.Graph{}
	err = dec.DecodeMessage(got, wire, structdec.DefaultOptions())
	require.NoError(t, err)

	assert.True(t, proto.Equal(orig, got), "expected Equal:\n  orig: %v\n  got:  %v", orig, got)
}

func TestDecodeOneof(t *testing.T) {
	t.Parallel()

	orig := &testpb.Oneof{
		Single: &testpb.Oneof_S1{S1: 42},
		Multi:  &testpb.Oneof_M2{M2: 999},
		Tail:   77,
	}

	wire, err := proto.Marshal(orig)
	require.NoError(t, err)

	dec, err := structdec.Get(orig)
	require.NoError(t, err)

	got := &testpb.Oneof{}
	err = dec.DecodeMessage(got, wire, structdec.DefaultOptions())
	require.NoError(t, err)

	assert.True(t, proto.Equal(orig, got), "expected Equal:\n  orig: %v\n  got:  %v", orig, got)
}

func TestDecodeMaps(t *testing.T) {
	t.Parallel()

	orig := &testpb.Maps{
		M10: map[int32]int32{1: 10, 2: 20},
		M1E: map[int32]string{10: "ten", 20: "twenty"},
		M1F: map[int32][]byte{1: []byte("one"), 2: []byte("two")},
	}

	wire, err := proto.Marshal(orig)
	require.NoError(t, err)

	dec, err := structdec.Get(orig)
	require.NoError(t, err)

	got := &testpb.Maps{}
	err = dec.DecodeMessage(got, wire, structdec.DefaultOptions())
	require.NoError(t, err)

	assert.True(t, proto.Equal(orig, got), "expected Equal:\n  orig: %v\n  got:  %v", orig, got)
}

func TestDecodeUnknownFields(t *testing.T) {
	t.Parallel()

	// Scalars with extra unknown field tag 999 = varint 42
	known := &testpb.Scalars{A1: 10}
	knownWire, err := proto.Marshal(known)
	require.NoError(t, err)

	extraWire := append(slices.Clone(knownWire), 0xb8, 0x3e, 0x2a) // tag 999<<3 | 0, value 42

	dec, err := structdec.Get(known)
	require.NoError(t, err)

	// Case 1: Keep unknown fields
	got1 := &testpb.Scalars{}
	err = dec.DecodeMessage(got1, extraWire, structdec.DefaultOptions())
	require.NoError(t, err)
	assert.Equal(t, int32(10), got1.A1)
	assert.NotEmpty(t, got1.ProtoReflect().GetUnknown())

	// Case 2: Discard unknown fields
	got2 := &testpb.Scalars{}
	opts := structdec.DefaultOptions()
	opts.DiscardUnknown = true
	err = dec.DecodeMessage(got2, extraWire, opts)
	require.NoError(t, err)
	assert.Equal(t, int32(10), got2.A1)
	assert.Empty(t, got2.ProtoReflect().GetUnknown())
}

func TestDecodeRequiredFields(t *testing.T) {
	t.Parallel()

	x := int32(42)
	req := &testpb.Required{
		X: &x,
		Z: &testpb.Required_Empty{},
	}
	wire, err := proto.Marshal(req)
	require.NoError(t, err)

	dec, err := structdec.Get(req)
	require.NoError(t, err)

	got := &testpb.Required{}
	err = dec.DecodeMessage(got, wire, structdec.DefaultOptions())
	require.NoError(t, err)
	assert.Equal(t, int32(42), *got.X)

	// Missing required field Z
	incompleteWire, err := proto.MarshalOptions{AllowPartial: true}.Marshal(&testpb.Required{X: &x})
	require.NoError(t, err)

	gotBad := &testpb.Required{}
	err = dec.DecodeMessage(gotBad, incompleteWire, structdec.DefaultOptions())
	require.Error(t, err)

	// Missing required field Z with AllowPartial: true -> succeeds
	gotPartial := &testpb.Required{}
	opts := structdec.DefaultOptions()
	opts.AllowPartial = true
	err = dec.DecodeMessage(gotPartial, incompleteWire, opts)
	require.NoError(t, err)
	assert.Equal(t, int32(42), *gotPartial.X)
}

func TestDecodeConformanceAllTypes(t *testing.T) {
	t.Parallel()

	orig := &conformance.TestAllTypesProto3{
		OptionalInt32:         100,
		OptionalInt64:         200,
		OptionalUint32:        300,
		OptionalUint64:        400,
		OptionalSint32:        -500,
		OptionalSint64:        -600,
		OptionalFixed32:       700,
		OptionalFixed64:       800,
		OptionalFloat:         900.5,
		OptionalDouble:        1000.5,
		OptionalBool:          true,
		OptionalString:        "proto3 string",
		OptionalBytes:         []byte("proto3 bytes"),
		OptionalNestedEnum:    conformance.TestAllTypesProto3_BAR,
		OptionalNestedMessage: &conformance.TestAllTypesProto3_NestedMessage{A: 55},
		RepeatedInt32:         []int32{1, 2, 3},
		RepeatedString:        []string{"a", "b"},
		MapStringString:       map[string]string{"k1": "v1", "k2": "v2"},
		OneofField:            &conformance.TestAllTypesProto3_OneofUint32{OneofUint32: 888},
	}

	wire, err := proto.Marshal(orig)
	require.NoError(t, err)

	dec, err := structdec.Get(orig)
	require.NoError(t, err)

	got := &conformance.TestAllTypesProto3{}
	err = dec.DecodeMessage(got, wire, structdec.DefaultOptions())
	require.NoError(t, err)

	assert.True(t, proto.Equal(orig, got), "expected Equal:\n  orig: %v\n  got:  %v", orig, got)
}

func BenchmarkDecodeScalars(b *testing.B) {
	msg := &testpb.Scalars{
		A1:  1,
		A2:  2,
		A3:  3,
		A4:  4,
		A5:  -5,
		A6:  -6,
		A7:  7,
		A8:  8,
		A9:  -9,
		A10: -10,
		A11: 11.5,
		A12: 12.5,
		A13: true,
		A14: "benchmark string test scalar",
		A15: []byte("benchmark bytes test scalar"),
	}
	wire, err := proto.Marshal(msg)
	require.NoError(b, err)

	dec, err := structdec.Get(msg)
	require.NoError(b, err)

	opts := structdec.DefaultOptions()
	opts.AllowAlias = true

	b.Run("proto.Unmarshal", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			dst := &testpb.Scalars{}
			if err := proto.Unmarshal(wire, dst); err != nil {
				b.Fatal(err)
			}
		}
	})

	b.Run("structdec.DecodeMessage", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			dst := &testpb.Scalars{}
			if err := dec.DecodeMessage(dst, wire, opts); err != nil {
				b.Fatal(err)
			}
		}
	})
}

func BenchmarkDecodeRepeated(b *testing.B) {
	orig := &testpb.Repeated{
		R1: []int32{1, 2, 3, 4, 5, 6, 7, 8, 9, 10},
		R2: []int64{10, 20, 30, 40, 50, 60, 70, 80, 90, 100},
		R3: []int32{-1, -2, -3, -4, -5},
		R4: []int64{-10, -20, -30, -40, -50},
		R5: []uint32{100, 200, 300, 400},
		R6: []uint64{1000, 2000, 3000, 4000},
		R7: []string{"foo", "bar", "baz", "qux"},
		R8: [][]byte{[]byte("bin1"), []byte("bin2")},
	}
	wire, err := proto.Marshal(orig)
	require.NoError(b, err)

	dec, err := structdec.Get(orig)
	require.NoError(b, err)

	opts := structdec.DefaultOptions()
	opts.AllowAlias = true

	b.Run("proto.Unmarshal", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			dst := &testpb.Repeated{}
			if err := proto.Unmarshal(wire, dst); err != nil {
				b.Fatal(err)
			}
		}
	})

	b.Run("structdec.DecodeMessage", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			dst := &testpb.Repeated{}
			if err := dec.DecodeMessage(dst, wire, opts); err != nil {
				b.Fatal(err)
			}
		}
	})
}

func BenchmarkDecodeGraph(b *testing.B) {
	orig := &testpb.Graph{
		V: 1,
		S: &testpb.Graph{
			V: 2,
			S: &testpb.Graph{
				V: 3,
			},
		},
		R: []*testpb.Graph{
			{V: 10},
			{V: 20, S: &testpb.Graph{V: 21}},
		},
	}
	wire, err := proto.Marshal(orig)
	require.NoError(b, err)

	dec, err := structdec.Get(orig)
	require.NoError(b, err)

	opts := structdec.DefaultOptions()
	opts.AllowAlias = true

	b.Run("proto.Unmarshal", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			dst := &testpb.Graph{}
			if err := proto.Unmarshal(wire, dst); err != nil {
				b.Fatal(err)
			}
		}
	})

	b.Run("structdec.DecodeMessage", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			dst := &testpb.Graph{}
			if err := dec.DecodeMessage(dst, wire, opts); err != nil {
				b.Fatal(err)
			}
		}
	})
}
