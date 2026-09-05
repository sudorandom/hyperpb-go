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

package structenc_test

import (
	"bytes"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	"buf.build/go/hyperpb/internal/gen/conformance"
	testpb "buf.build/go/hyperpb/internal/gen/test"
	"buf.build/go/hyperpb/internal/structdec"
	"buf.build/go/hyperpb/internal/structenc"
)

func TestEncodeScalars(t *testing.T) {
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

	enc, err := structenc.Get(orig)
	require.NoError(t, err)

	gotWire, err := enc.EncodeMessage(orig, nil, structenc.DefaultOptions())
	require.NoError(t, err)

	wantWire, err := proto.Marshal(orig)
	require.NoError(t, err)

	assert.Equal(t, wantWire, gotWire)

	// Verify decode roundtrip
	gotMsg := &testpb.Scalars{}
	dec, err := structdec.Get(gotMsg)
	require.NoError(t, err)
	err = dec.DecodeMessage(gotMsg, gotWire, structdec.DefaultOptions())
	require.NoError(t, err)
	assert.True(t, proto.Equal(orig, gotMsg))
}

func TestEncodeSubMessages(t *testing.T) {
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

	enc, err := structenc.Get(orig)
	require.NoError(t, err)

	gotWire, err := enc.EncodeMessage(orig, nil, structenc.DefaultOptions())
	require.NoError(t, err)

	wantWire, err := proto.Marshal(orig)
	require.NoError(t, err)

	assert.Equal(t, wantWire, gotWire)

	// Roundtrip
	gotMsg := &testpb.Graph{}
	dec, err := structdec.Get(gotMsg)
	require.NoError(t, err)
	err = dec.DecodeMessage(gotMsg, gotWire, structdec.DefaultOptions())
	require.NoError(t, err)
	assert.True(t, proto.Equal(orig, gotMsg))
}

func TestEncodeRepeated(t *testing.T) {
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

	enc, err := structenc.Get(orig)
	require.NoError(t, err)

	gotWire, err := enc.EncodeMessage(orig, nil, structenc.DefaultOptions())
	require.NoError(t, err)

	wantWire, err := proto.Marshal(orig)
	require.NoError(t, err)

	assert.Equal(t, wantWire, gotWire)

	// Roundtrip
	gotMsg := &testpb.Repeated{}
	dec, err := structdec.Get(gotMsg)
	require.NoError(t, err)
	err = dec.DecodeMessage(gotMsg, gotWire, structdec.DefaultOptions())
	require.NoError(t, err)
	assert.True(t, proto.Equal(orig, gotMsg))
}

func TestEncodeOneofs(t *testing.T) {
	t.Parallel()

	tests := []*testpb.Oneof{
		{Single: &testpb.Oneof_S1{S1: 42}},
		{Multi: &testpb.Oneof_M8{M8: "hello"}},
		{Multi: &testpb.Oneof_M2{M2: 999}},
		{Single: &testpb.Oneof_S1{S1: 0}}, // presence test with zero
		{Multi: &testpb.Oneof_M8{M8: ""}},
		{Multi: &testpb.Oneof_M10{M10: &testpb.Oneof{Tail: 55}}},
	}

	for _, orig := range tests {
		enc, err := structenc.Get(orig)
		require.NoError(t, err)

		gotWire, err := enc.EncodeMessage(orig, nil, structenc.DefaultOptions())
		require.NoError(t, err)

		wantWire, err := proto.Marshal(orig)
		require.NoError(t, err)

		assert.Equal(t, wantWire, gotWire)

		gotMsg := &testpb.Oneof{}
		dec, err := structdec.Get(gotMsg)
		require.NoError(t, err)
		err = dec.DecodeMessage(gotMsg, gotWire, structdec.DefaultOptions())
		require.NoError(t, err)
		assert.True(t, proto.Equal(orig, gotMsg))
	}
}

func TestEncodeMaps(t *testing.T) {
	t.Parallel()

	orig := &testpb.Maps{
		M10: map[int32]int32{1: 10, 2: 20},
		M1E: map[int32]string{10: "ten", 20: "twenty"},
		M1F: map[int32][]byte{1: []byte("one"), 2: []byte("two")},
	}

	enc, err := structenc.Get(orig)
	require.NoError(t, err)

	opts := structenc.DefaultOptions()
	opts.Deterministic = true

	wire1, err := enc.EncodeMessage(orig, nil, opts)
	require.NoError(t, err)

	wire2, err := enc.EncodeMessage(orig, nil, opts)
	require.NoError(t, err)

	assert.True(t, bytes.Equal(wire1, wire2), "deterministic encoding must match across runs")

	// Roundtrip through standard proto.Unmarshal
	gotMsg := &testpb.Maps{}
	err = proto.Unmarshal(wire1, gotMsg)
	require.NoError(t, err)
	assert.True(t, proto.Equal(orig, gotMsg))
}

func TestEncodeUnknownFields(t *testing.T) {
	t.Parallel()

	// Scalars with extra unknown field tag 999 = varint 42
	known := &testpb.Scalars{A1: 10}
	knownWire, err := proto.Marshal(known)
	require.NoError(t, err)
	extraWire := append(slices.Clone(knownWire), 0xb8, 0x3e, 0x2a)

	dec, err := structdec.Get(known)
	require.NoError(t, err)

	decoded := &testpb.Scalars{}
	err = dec.DecodeMessage(decoded, extraWire, structdec.DefaultOptions())
	require.NoError(t, err)

	// Re-encode with structenc
	enc, err := structenc.Get(decoded)
	require.NoError(t, err)
	reencoded, err := enc.EncodeMessage(decoded, nil, structenc.DefaultOptions())
	require.NoError(t, err)

	assert.Equal(t, extraWire, reencoded)
}

func TestEncodeRequiredFields(t *testing.T) {
	t.Parallel()

	req := &testpb.Required{}

	enc, err := structenc.Get(req)
	require.NoError(t, err)

	// Without AllowPartial -> should error with ErrRequiredNotSet
	_, err = enc.EncodeMessage(req, nil, structenc.DefaultOptions())
	require.Error(t, err)
	require.ErrorIs(t, err, structenc.ErrRequiredNotSet)

	// With AllowPartial -> succeeds
	opts := structenc.DefaultOptions()
	opts.AllowPartial = true
	b, err := enc.EncodeMessage(req, nil, opts)
	require.NoError(t, err)
	assert.Empty(t, b)

	// Now set required fields
	x := int32(42)
	req.X = &x
	req.Z = &testpb.Required_Empty{}
	b, err = enc.EncodeMessage(req, nil, structenc.DefaultOptions())
	require.NoError(t, err)
	assert.NotEmpty(t, b)
}

func TestEncodeConformanceProto3(t *testing.T) {
	t.Parallel()

	orig := &conformance.TestAllTypesProto3{
		OptionalInt32:  123,
		OptionalInt64:  456,
		OptionalString: "testing proto3 conformance",
		OptionalBytes:  []byte("hello world"),
		OptionalNestedMessage: &conformance.TestAllTypesProto3_NestedMessage{
			A: 999,
		},
		RepeatedInt32:   []int32{1, 2, 3, 4, 5},
		RepeatedString:  []string{"a", "b", "c"},
		MapStringString: map[string]string{"k1": "v1", "k2": "v2"},
		OneofField:      &conformance.TestAllTypesProto3_OneofUint32{OneofUint32: 777},
	}

	enc, err := structenc.Get(orig)
	require.NoError(t, err)

	wire, err := enc.EncodeMessage(orig, nil, structenc.DefaultOptions())
	require.NoError(t, err)

	got := &conformance.TestAllTypesProto3{}
	err = proto.Unmarshal(wire, got)
	require.NoError(t, err)

	assert.True(t, proto.Equal(orig, got))
}

func BenchmarkEncodeScalars(b *testing.B) {
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

	enc, err := structenc.Get(orig)
	require.NoError(b, err)

	opts := structenc.DefaultOptions()
	buf := make([]byte, 0, 128)

	b.Run("proto.Marshal", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			_, err := proto.Marshal(orig)
			if err != nil {
				b.Fatal(err)
			}
		}
	})

	b.Run("structenc.EncodeMessage", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			_, err := enc.EncodeMessage(orig, buf[:0], opts)
			if err != nil {
				b.Fatal(err)
			}
		}
	})
}

func BenchmarkEncodeGraph(b *testing.B) {
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

	enc, err := structenc.Get(orig)
	require.NoError(b, err)

	opts := structenc.DefaultOptions()
	buf := make([]byte, 0, 128)

	b.Run("proto.Marshal", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			_, err := proto.Marshal(orig)
			if err != nil {
				b.Fatal(err)
			}
		}
	})

	b.Run("structenc.EncodeMessage", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			_, err := enc.EncodeMessage(orig, buf[:0], opts)
			if err != nil {
				b.Fatal(err)
			}
		}
	})
}
