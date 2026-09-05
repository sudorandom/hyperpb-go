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

package hyperjson_test

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	"buf.build/go/hyperpb"
	"buf.build/go/hyperpb/hyperjson"
	conformance "buf.build/go/hyperpb/internal/gen/conformance"
	testpb "buf.build/go/hyperpb/internal/gen/test"
)

// makeFullScalars creates a testpb.Scalars message with all 15 proto3 (no-presence)
// scalars and all 15 proto3 optional (with presence) scalars populated with distinct values.
func makeFullScalars() *testpb.Scalars {
	b1 := int32(201)
	b2 := int64(202)
	b3 := uint32(203)
	b4 := uint64(204)
	b5 := int32(205)
	b6 := int64(206)
	b7 := uint32(207)
	b8 := uint64(208)
	b9 := int32(209)
	b10 := int64(210)
	b11 := float32(211.5)
	b12 := float64(212.5)
	b13 := true
	b14 := "optional string initial"
	b15 := []byte("optional bytes initial")

	return &testpb.Scalars{
		A1:  101,
		A2:  102,
		A3:  103,
		A4:  104,
		A5:  105,
		A6:  106,
		A7:  107,
		A8:  108,
		A9:  109,
		A10: 110,
		A11: 111.5,
		A12: 112.5,
		A13: true,
		A14: "proto3 string initial",
		A15: []byte("proto3 bytes initial"),

		B1:  &b1,
		B2:  &b2,
		B3:  &b3,
		B4:  &b4,
		B5:  &b5,
		B6:  &b6,
		B7:  &b7,
		B8:  &b8,
		B9:  &b9,
		B10: &b10,
		B11: &b11,
		B12: &b12,
		B13: &b13,
		B14: &b14,
		B15: b15,
	}
}

// makeFullOneof creates a testpb.Oneof message with populated single, multi, and tail fields.
func makeFullOneof() *testpb.Oneof {
	return &testpb.Oneof{
		Single: &testpb.Oneof_S1{S1: 42},
		Multi:  &testpb.Oneof_M8{M8: "initial oneof string"},
		Tail:   999,
	}
}

// makeFullGraph creates a nested recursive testpb.Graph message.
func makeFullGraph() *testpb.Graph {
	return &testpb.Graph{
		V: 1,
		S: &testpb.Graph{
			V: 2,
			S: &testpb.Graph{V: 3},
		},
		R: []*testpb.Graph{
			{V: 10},
			{V: 20, S: &testpb.Graph{V: 30}},
		},
	}
}

// makeFullMaps creates a populated testpb.Maps message.
func makeFullMaps() *testpb.Maps {
	return &testpb.Maps{
		M10: map[int32]int32{1: 10, 2: 20},
		M11: map[int32]int64{3: 30},
		M1E: map[int32]string{4: "four", 5: "five"},
		M1F: map[int32][]byte{6: []byte("six")},
		M20: map[int64]int32{7: 70},
		M30: map[uint32]int32{8: 80},
		M40: map[uint64]int32{9: 90},
	}
}

// makeFullConformanceTestAllTypes creates a conformance.TestAllTypesProto3 with
// singular scalars, repeated fields, maps, submessages, and oneofs populated.
func makeFullConformanceTestAllTypes() *conformance.TestAllTypesProto3 {
	return &conformance.TestAllTypesProto3{
		OptionalInt32:    101,
		OptionalInt64:    102,
		OptionalUint32:   103,
		OptionalUint64:   104,
		OptionalSint32:   105,
		OptionalSint64:   106,
		OptionalFixed32:  107,
		OptionalFixed64:  108,
		OptionalSfixed32: 109,
		OptionalSfixed64: 110,
		OptionalFloat:    111.5,
		OptionalDouble:   112.5,
		OptionalBool:     true,
		OptionalString:   "hello kitchen sink",
		OptionalBytes:    []byte("bytes kitchen sink"),
		OptionalNestedMessage: &conformance.TestAllTypesProto3_NestedMessage{
			A: 42,
		},
		OptionalForeignMessage: &conformance.ForeignMessage{
			C: 43,
		},
		OptionalNestedEnum:  conformance.TestAllTypesProto3_BAR,
		OptionalForeignEnum: conformance.ForeignEnum_FOREIGN_BAR,

		RepeatedInt32:    []int32{1, 2, 3},
		RepeatedInt64:    []int64{10, 20, 30},
		RepeatedUint32:   []uint32{100, 200},
		RepeatedUint64:   []uint64{1000, 2000},
		RepeatedSint32:   []int32{-1, -2},
		RepeatedSint64:   []int64{-10, -20},
		RepeatedFixed32:  []uint32{5, 6},
		RepeatedFixed64:  []uint64{50, 60},
		RepeatedSfixed32: []int32{-5, -6},
		RepeatedSfixed64: []int64{-50, -60},
		RepeatedFloat:    []float32{1.1, 2.2},
		RepeatedDouble:   []float64{3.3, 4.4},
		RepeatedBool:     []bool{true, false, true},
		RepeatedString:   []string{"foo", "bar"},
		RepeatedBytes:    [][]byte{[]byte("baz"), []byte("qux")},
		RepeatedNestedMessage: []*conformance.TestAllTypesProto3_NestedMessage{
			{A: 100},
			{A: 200},
		},

		MapInt32Int32:   map[int32]int32{1: 10, 2: 20},
		MapInt64Int64:   map[int64]int64{100: 1000},
		MapUint32Uint32: map[uint32]uint32{7: 77},
		MapUint64Uint64: map[uint64]uint64{8: 88},
		MapStringString: map[string]string{"key1": "val1", "key2": "val2"},
		MapStringBytes:  map[string][]byte{"k1": []byte("v1")},

		OneofField: &conformance.TestAllTypesProto3_OneofString{
			OneofString: "initial oneof string",
		},
	}
}

// TestKitchenSink_FreshMessage validates that freshly allocated (unparsed) messages
// correctly handle all mutation, presence, scalar-to-zero, oneof, repeated, map,
// submessage, and wire/JSON serialization operations.
func TestKitchenSink_FreshMessage(t *testing.T) {
	t.Parallel()

	t.Run("Scalars_Proto3ZeroVsNonZeroAndPresence", func(t *testing.T) {
		t.Parallel()
		md := mdOf(&testpb.Scalars{})
		ct := compileFor(t, md)
		fds := md.Fields()

		hm := hyperpb.NewMessage(ct)

		// 1. Initially empty: no fields present
		for i := range fds.Len() {
			assert.False(t, hm.Has(fds.Get(i)), "unpopulated field %s should not have presence", fds.Get(i).Name())
		}
		rangeCount := 0
		hm.Range(func(fd protoreflect.FieldDescriptor, val protoreflect.Value) bool {
			rangeCount++
			return true
		})
		assert.Equal(t, 0, rangeCount, "Range on fresh message should yield 0 fields")

		wire, err := proto.Marshal(hm)
		require.NoError(t, err)
		assert.Empty(t, wire, "fresh message should marshal to empty wire")

		jsonBytes, err := hyperjson.Marshal(hm)
		require.NoError(t, err)
		assert.JSONEq(t, "{}", string(jsonBytes), "fresh message should marshal to empty JSON object")

		// 2. Set proto3 non-presence scalar to zero: must NOT have presence
		fdA1 := fds.ByName("a1")
		hm.Set(fdA1, protoreflect.ValueOfInt32(0))
		assert.False(t, hm.Has(fdA1), "proto3 scalar set to 0 must report Has=false")

		fdA14 := fds.ByName("a14")
		hm.Set(fdA14, protoreflect.ValueOfString(""))
		assert.False(t, hm.Has(fdA14), "proto3 string set to empty must report Has=false")

		fdA15 := fds.ByName("a15")
		hm.Set(fdA15, protoreflect.ValueOfBytes(nil))
		assert.False(t, hm.Has(fdA15), "proto3 bytes set to nil must report Has=false")

		wire, err = proto.Marshal(hm)
		require.NoError(t, err)
		assert.Empty(t, wire, "proto3 zero-value scalars must be omitted from wire")

		jsonBytes, err = hyperjson.Marshal(hm)
		require.NoError(t, err)
		assert.JSONEq(t, "{}", string(jsonBytes), "proto3 zero-value scalars must be omitted from JSON")

		// 3. Set proto3 optional (with presence) scalar to zero: MUST have presence
		fdB1 := fds.ByName("b1")
		hm.Set(fdB1, protoreflect.ValueOfInt32(0))
		assert.True(t, hm.Has(fdB1), "field with presence set to 0 must report Has=true")

		fdB14 := fds.ByName("b14")
		hm.Set(fdB14, protoreflect.ValueOfString(""))
		assert.True(t, hm.Has(fdB14), "optional string set to empty string must report Has=true")

		wire, err = proto.Marshal(hm)
		require.NoError(t, err)
		assert.NotEmpty(t, wire, "fields with presence set to zero must emit to wire")

		jsonBytes, err = hyperjson.Marshal(hm)
		require.NoError(t, err)
		assert.Contains(t, string(jsonBytes), `"b1":0`, "optional scalar set to 0 must be in JSON: %s", jsonBytes)
		assert.Contains(t, string(jsonBytes), `"b14":""`, "optional string set to empty must be in JSON: %s", jsonBytes)

		// 4. Set all 15 non-presence scalars to non-zero values
		hm.Set(fds.ByName("a1"), protoreflect.ValueOfInt32(42))
		hm.Set(fds.ByName("a2"), protoreflect.ValueOfInt64(43))
		hm.Set(fds.ByName("a3"), protoreflect.ValueOfUint32(44))
		hm.Set(fds.ByName("a4"), protoreflect.ValueOfUint64(45))
		hm.Set(fds.ByName("a5"), protoreflect.ValueOfInt32(-46))
		hm.Set(fds.ByName("a6"), protoreflect.ValueOfInt64(-47))
		hm.Set(fds.ByName("a7"), protoreflect.ValueOfUint32(48))
		hm.Set(fds.ByName("a8"), protoreflect.ValueOfUint64(49))
		hm.Set(fds.ByName("a9"), protoreflect.ValueOfInt32(50))
		hm.Set(fds.ByName("a10"), protoreflect.ValueOfInt64(51))
		hm.Set(fds.ByName("a11"), protoreflect.ValueOfFloat32(52.5))
		hm.Set(fds.ByName("a12"), protoreflect.ValueOfFloat64(53.5))
		hm.Set(fds.ByName("a13"), protoreflect.ValueOfBool(true))
		hm.Set(fds.ByName("a14"), protoreflect.ValueOfString("test string"))
		hm.Set(fds.ByName("a15"), protoreflect.ValueOfBytes([]byte("test bytes")))

		for _, name := range []string{"a1", "a2", "a3", "a4", "a5", "a6", "a7", "a8", "a9", "a10", "a11", "a12", "a13", "a14", "a15"} {
			assert.True(t, hm.Has(fds.ByName(protoreflect.Name(name))), "field %s should have presence", name)
		}

		wire, err = proto.Marshal(hm)
		require.NoError(t, err)
		parsedWire := &testpb.Scalars{}
		require.NoError(t, proto.Unmarshal(wire, parsedWire))
		assert.EqualValues(t, 42, parsedWire.GetA1())
		assert.Equal(t, "test string", parsedWire.GetA14())
		assert.Equal(t, []byte("test bytes"), parsedWire.GetA15())

		jsonBytes, err = hyperjson.Marshal(hm)
		require.NoError(t, err)
		parsedJSON := &testpb.Scalars{}
		require.NoError(t, protojson.Unmarshal(jsonBytes, parsedJSON))
		assert.EqualValues(t, 42, parsedJSON.GetA1())
		assert.Equal(t, "test string", parsedJSON.GetA14())
		assert.Equal(t, []byte("test bytes"), parsedJSON.GetA15())
	})

	t.Run("Oneof_Transitions", func(t *testing.T) {
		t.Parallel()
		md := mdOf(&testpb.Oneof{})
		ct := compileFor(t, md)
		fds := md.Fields()

		hm := hyperpb.NewMessage(ct)
		fdM1 := fds.ByName("m1") // int32
		fdM8 := fds.ByName("m8") // string
		fdTail := fds.ByName("tail")

		hm.Set(fdTail, protoreflect.ValueOfInt32(777))
		assert.True(t, hm.Has(fdTail))

		// Set branch 1: m1
		hm.Set(fdM1, protoreflect.ValueOfInt32(123))
		assert.True(t, hm.Has(fdM1))
		assert.False(t, hm.Has(fdM8))

		// Switch to branch 2: m8
		hm.Set(fdM8, protoreflect.ValueOfString("hello oneof"))
		assert.False(t, hm.Has(fdM1), "previous oneof branch must be deactivated")
		assert.True(t, hm.Has(fdM8), "new oneof branch must be active")

		wire, err := proto.Marshal(hm)
		require.NoError(t, err)
		parsed := &testpb.Oneof{}
		require.NoError(t, proto.Unmarshal(wire, parsed))
		assert.Equal(t, "hello oneof", parsed.GetM8())
		assert.Zero(t, parsed.GetM1())
		assert.EqualValues(t, 777, parsed.GetTail())

		jsonBytes, err := hyperjson.Marshal(hm)
		require.NoError(t, err)
		assert.Contains(t, string(jsonBytes), `"m8":"hello oneof"`)
		assert.NotContains(t, string(jsonBytes), `"m1"`)
	})

	t.Run("Repeated_And_Maps", func(t *testing.T) {
		t.Parallel()
		md := mdOf(&conformance.TestAllTypesProto3{})
		ct := compileFor(t, md)
		fds := md.Fields()

		hm := hyperpb.NewMessage(ct)

		// Repeated operations
		fdR1 := fds.ByName("repeated_int32")
		rList := hm.Mutable(fdR1).List()
		for i := 1; i <= 5; i++ {
			rList.Append(protoreflect.ValueOfInt32(int32(i * 10)))
		}
		assert.Equal(t, 5, rList.Len())
		rList.Set(0, protoreflect.ValueOfInt32(999))
		assert.EqualValues(t, 999, rList.Get(0).Int())

		rList.Truncate(3)
		assert.Equal(t, 3, rList.Len())
		assert.EqualValues(t, 999, rList.Get(0).Int())
		assert.EqualValues(t, 20, rList.Get(1).Int())
		assert.EqualValues(t, 30, rList.Get(2).Int())

		// Map operations
		fdM := fds.ByName("map_string_string")
		mp := hm.Mutable(fdM).Map()
		mp.Set(protoreflect.MapKey(protoreflect.ValueOfString("keyA")), protoreflect.ValueOfString("valA"))
		mp.Set(protoreflect.MapKey(protoreflect.ValueOfString("keyB")), protoreflect.ValueOfString("valB"))
		assert.True(t, mp.Has(protoreflect.MapKey(protoreflect.ValueOfString("keyA"))))
		assert.Equal(t, "valA", mp.Get(protoreflect.MapKey(protoreflect.ValueOfString("keyA"))).String())

		// Overwrite key
		mp.Set(protoreflect.MapKey(protoreflect.ValueOfString("keyA")), protoreflect.ValueOfString("valA_updated"))
		assert.Equal(t, "valA_updated", mp.Get(protoreflect.MapKey(protoreflect.ValueOfString("keyA"))).String())

		// Delete key
		mp.Clear(protoreflect.MapKey(protoreflect.ValueOfString("keyB")))
		assert.False(t, mp.Has(protoreflect.MapKey(protoreflect.ValueOfString("keyB"))))

		// Verify wire and JSON roundtrip
		wire, err := proto.Marshal(hm)
		require.NoError(t, err)
		parsedWire := &conformance.TestAllTypesProto3{}
		require.NoError(t, proto.Unmarshal(wire, parsedWire))
		assert.Equal(t, []int32{999, 20, 30}, parsedWire.GetRepeatedInt32())
		assert.Equal(t, map[string]string{"keyA": "valA_updated"}, parsedWire.GetMapStringString())

		jsonBytes, err := hyperjson.Marshal(hm)
		require.NoError(t, err)
		parsedJSON := &conformance.TestAllTypesProto3{}
		require.NoError(t, protojson.Unmarshal(jsonBytes, parsedJSON))
		assert.Equal(t, []int32{999, 20, 30}, parsedJSON.GetRepeatedInt32())
		assert.Equal(t, map[string]string{"keyA": "valA_updated"}, parsedJSON.GetMapStringString())
	})

	t.Run("Graph_Recursive", func(t *testing.T) {
		t.Parallel()
		md := mdOf(&testpb.Graph{})
		ct := compileFor(t, md)
		fds := md.Fields()

		hm := hyperpb.NewMessage(ct)
		hm.Set(fds.ByName("v"), protoreflect.ValueOfInt32(1))

		sub1 := hm.Mutable(fds.ByName("s")).Message()
		sub1.Set(fds.ByName("v"), protoreflect.ValueOfInt32(2))

		sub2 := sub1.Mutable(fds.ByName("s")).Message()
		sub2.Set(fds.ByName("v"), protoreflect.ValueOfInt32(3))

		wire, err := proto.Marshal(hm)
		require.NoError(t, err)
		parsedWire := &testpb.Graph{}
		require.NoError(t, proto.Unmarshal(wire, parsedWire))
		assert.EqualValues(t, 1, parsedWire.GetV())
		require.NotNil(t, parsedWire.GetS())
		assert.EqualValues(t, 2, parsedWire.GetS().GetV())
		require.NotNil(t, parsedWire.GetS().GetS())
		assert.EqualValues(t, 3, parsedWire.GetS().GetS().GetV())

		jsonBytes, err := hyperjson.Marshal(hm)
		require.NoError(t, err)
		parsedJSON := &testpb.Graph{}
		require.NoError(t, protojson.Unmarshal(jsonBytes, parsedJSON))
		assert.EqualValues(t, 1, parsedJSON.GetV())
		assert.EqualValues(t, 2, parsedJSON.GetS().GetV())
		assert.EqualValues(t, 3, parsedJSON.GetS().GetS().GetV())
	})
}

// TestKitchenSink_EmptyMessageFromWire validates that messages parsed from an
// empty wire payload ([]byte{}) start in a pristine unpopulated state and behave
// identically to fresh messages under mutation, presence, and serialization.
func TestKitchenSink_EmptyMessageFromWire(t *testing.T) {
	t.Parallel()

	md := mdOf(&testpb.Scalars{})
	ct := compileFor(t, md)
	fds := md.Fields()

	hm := hyperpb.NewMessage(ct)
	require.NoError(t, hm.Unmarshal([]byte{}))

	// Initially empty
	for i := range fds.Len() {
		assert.False(t, hm.Has(fds.Get(i)), "unpopulated field %s should report Has=false", fds.Get(i).Name())
	}
	rangeCount := 0
	hm.Range(func(fd protoreflect.FieldDescriptor, val protoreflect.Value) bool {
		rangeCount++
		return true
	})
	assert.Equal(t, 0, rangeCount, "Range on empty wire message should yield 0 fields")

	wire, err := proto.Marshal(hm)
	require.NoError(t, err)
	assert.Empty(t, wire)

	jsonBytes, err := hyperjson.Marshal(hm)
	require.NoError(t, err)
	assert.JSONEq(t, "{}", string(jsonBytes))

	// Mutating scalar to 0 (proto3 no presence) must not set presence
	hm.Set(fds.ByName("a1"), protoreflect.ValueOfInt32(0))
	assert.False(t, hm.Has(fds.ByName("a1")), "proto3 scalar set to 0 must report Has=false")

	// Mutating scalar to non-zero
	hm.Set(fds.ByName("a1"), protoreflect.ValueOfInt32(77))
	assert.True(t, hm.Has(fds.ByName("a1")), "proto3 scalar set to non-zero must report Has=true")

	// Mutating optional scalar to 0 (with presence)
	hm.Set(fds.ByName("b1"), protoreflect.ValueOfInt32(0))
	assert.True(t, hm.Has(fds.ByName("b1")), "field with presence set to 0 must report Has=true")

	wire, err = proto.Marshal(hm)
	require.NoError(t, err)
	parsedWire := &testpb.Scalars{}
	require.NoError(t, proto.Unmarshal(wire, parsedWire))
	assert.EqualValues(t, 77, parsedWire.GetA1())
	require.NotNil(t, parsedWire.B1)
	assert.EqualValues(t, 0, *parsedWire.B1)

	jsonBytes, err = hyperjson.Marshal(hm)
	require.NoError(t, err)
	parsedJSON := &testpb.Scalars{}
	require.NoError(t, protojson.Unmarshal(jsonBytes, parsedJSON))
	assert.EqualValues(t, 77, parsedJSON.GetA1())
	require.NotNil(t, parsedJSON.B1)
	assert.EqualValues(t, 0, *parsedJSON.B1)
}

// TestKitchenSink_FullMessageFromWireThenMutated parses a completely populated
// message from the wire, verifies unmutated behavior, and then performs comprehensive
// mutations across every field type:
// - Overwrite non-zero scalars with new values
// - Overwrite scalars with ZERO values (verifying proto3 presence suppression & arena shadowing!)
// - Overwrite optional scalars with zero (verifying presence retention!)
// - Switch oneof branches
// - Mutate repeated lists (Set, Append, Truncate)
// - Mutate maps (Set, Overwrite, Clear)
// - Mutate nested submessages
// - Clear fields
// And verifies against an identically mutated reference message.
func TestKitchenSink_FullMessageFromWireThenMutated(t *testing.T) {
	t.Parallel()

	t.Run("Scalars_ArenaShadowingAndZeroing", func(t *testing.T) {
		t.Parallel()
		orig := makeFullScalars()
		wire, err := proto.Marshal(orig)
		require.NoError(t, err)

		md := mdOf(orig)
		ct := compileFor(t, md)
		fds := md.Fields()

		hm := hyperpb.NewMessage(ct)
		require.NoError(t, hm.Unmarshal(wire))

		// 1. Verify unmutated state
		for _, name := range []string{"a1", "a2", "a14", "a15", "b1", "b14", "b15"} {
			assert.True(t, hm.Has(fds.ByName(protoreflect.Name(name))))
		}
		unmutWire, err := proto.Marshal(hm)
		require.NoError(t, err)
		assert.True(t, proto.Equal(orig, mustUnmarshalScalars(t, unmutWire)))

		unmutJSON, err := hyperjson.Marshal(hm)
		require.NoError(t, err)
		refJSON, err := protojson.Marshal(orig)
		require.NoError(t, err)
		assert.JSONEq(t, string(refJSON), string(unmutJSON))

		// 2. Mutate: overwrite some with new values, some with zero, some cleared
		hm.Set(fds.ByName("a1"), protoreflect.ValueOfInt32(9999)) // new non-zero
		hm.Set(fds.ByName("a2"), protoreflect.ValueOfInt64(0))    // proto3 non-presence -> ZERO! Must shadow arena!
		hm.Set(fds.ByName("a14"), protoreflect.ValueOfString("")) // proto3 string -> ZERO! Must shadow arena!
		hm.Clear(fds.ByName("a3"))                                // explicit Clear

		hm.Set(fds.ByName("b1"), protoreflect.ValueOfInt32(0)) // proto3 optional -> ZERO! Must keep presence!
		hm.Clear(fds.ByName("b2"))                             // explicit Clear on optional

		// Verify Has semantics
		assert.True(t, hm.Has(fds.ByName("a1")), "a1 has non-zero value, Has must be true")
		assert.False(t, hm.Has(fds.ByName("a2")), "a2 was set to 0, Has must be false")
		assert.False(t, hm.Has(fds.ByName("a14")), "a14 was set to '', Has must be false")
		assert.False(t, hm.Has(fds.ByName("a3")), "a3 was cleared, Has must be false")
		assert.True(t, hm.Has(fds.ByName("b1")), "b1 is optional set to 0, Has must be true")
		assert.False(t, hm.Has(fds.ByName("b2")), "b2 was cleared, Has must be false")

		// Verify Range does not yield a2, a14, a3, b2
		hm.Range(func(fd protoreflect.FieldDescriptor, val protoreflect.Value) bool {
			assert.NotEqual(t, "a2", string(fd.Name()), "a2 (set to 0) must not be in Range")
			assert.NotEqual(t, "a14", string(fd.Name()), "a14 (set to '') must not be in Range")
			assert.NotEqual(t, "a3", string(fd.Name()), "a3 (cleared) must not be in Range")
			assert.NotEqual(t, "b2", string(fd.Name()), "b2 (cleared) must not be in Range")
			if fd.Name() == "a1" {
				assert.EqualValues(t, 9999, val.Int())
			}
			if fd.Name() == "b1" {
				assert.EqualValues(t, 0, val.Int())
			}
			return true
		})

		// Wire serialization must reflect mutations
		mutWire, err := proto.Marshal(hm)
		require.NoError(t, err)
		parsedMutWire := mustUnmarshalScalars(t, mutWire)
		assert.EqualValues(t, 9999, parsedMutWire.GetA1())
		assert.Zero(t, parsedMutWire.GetA2())
		assert.Empty(t, parsedMutWire.GetA14())
		assert.Zero(t, parsedMutWire.GetA3())
		require.NotNil(t, parsedMutWire.B1)
		assert.EqualValues(t, 0, *parsedMutWire.B1)
		assert.Nil(t, parsedMutWire.B2)

		// JSON serialization must reflect mutations
		mutJSON, err := hyperjson.Marshal(hm)
		require.NoError(t, err)
		parsedMutJSON := &testpb.Scalars{}
		require.NoError(t, protojson.Unmarshal(mutJSON, parsedMutJSON))
		assert.EqualValues(t, 9999, parsedMutJSON.GetA1())
		assert.Zero(t, parsedMutJSON.GetA2())
		assert.Empty(t, parsedMutJSON.GetA14())
		assert.Zero(t, parsedMutJSON.GetA3())
		require.NotNil(t, parsedMutJSON.B1)
		assert.EqualValues(t, 0, *parsedMutJSON.B1)
		assert.Nil(t, parsedMutJSON.B2)

		assert.Contains(t, string(mutJSON), `"a1":9999`)
		assert.NotContains(t, string(mutJSON), `"a2"`)
		assert.NotContains(t, string(mutJSON), `"a14"`)
		assert.NotContains(t, string(mutJSON), `"a3"`)
		assert.Contains(t, string(mutJSON), `"b1":0`)
		assert.NotContains(t, string(mutJSON), `"b2"`)
	})

	t.Run("Oneof_SwitchingFromWire", func(t *testing.T) {
		t.Parallel()
		orig := makeFullOneof() // Multi has M8: "initial oneof string"
		wire, err := proto.Marshal(orig)
		require.NoError(t, err)

		md := mdOf(orig)
		ct := compileFor(t, md)
		fds := md.Fields()

		hm := hyperpb.NewMessage(ct)
		require.NoError(t, hm.Unmarshal(wire))
		assert.True(t, hm.Has(fds.ByName("m8")))
		assert.False(t, hm.Has(fds.ByName("m1")))

		// Switch oneof from m8 to m1
		hm.Set(fds.ByName("m1"), protoreflect.ValueOfInt32(5555))
		assert.False(t, hm.Has(fds.ByName("m8")), "old oneof branch must be deactivated")
		assert.True(t, hm.Has(fds.ByName("m1")), "new oneof branch must be active")

		wireMut, err := proto.Marshal(hm)
		require.NoError(t, err)
		parsed := &testpb.Oneof{}
		require.NoError(t, proto.Unmarshal(wireMut, parsed))
		assert.EqualValues(t, 5555, parsed.GetM1())
		assert.Empty(t, parsed.GetM8())

		jsonMut, err := hyperjson.Marshal(hm)
		require.NoError(t, err)
		assert.Contains(t, string(jsonMut), `"m1":5555`)
		assert.NotContains(t, string(jsonMut), `"m8"`)
	})

	t.Run("FullConformanceTestAllTypes_Mutated", func(t *testing.T) {
		t.Parallel()
		orig := makeFullConformanceTestAllTypes()
		wire, err := proto.Marshal(orig)
		require.NoError(t, err)

		md := mdOf(orig)
		ct := compileFor(t, md)
		fds := md.Fields()

		hm := hyperpb.NewMessage(ct)
		require.NoError(t, hm.Unmarshal(wire))

		// Mutate scalars
		hm.Set(fds.ByName("optional_int32"), protoreflect.ValueOfInt32(7777))
		hm.Set(fds.ByName("optional_int64"), protoreflect.ValueOfInt64(0)) // set to 0: must shadow wire!
		hm.Set(fds.ByName("optional_string"), protoreflect.ValueOfString("mutated string"))

		// Mutate repeated
		rList := hm.Mutable(fds.ByName("repeated_int32")).List()
		rList.Append(protoreflect.ValueOfInt32(999))
		rList.Set(0, protoreflect.ValueOfInt32(1111))

		// Mutate map
		mMap := hm.Mutable(fds.ByName("map_string_string")).Map()
		mMap.Set(protoreflect.MapKey(protoreflect.ValueOfString("key_new")), protoreflect.ValueOfString("val_new"))
		mMap.Clear(protoreflect.MapKey(protoreflect.ValueOfString("key1")))

		// Switch oneof
		hm.Set(fds.ByName("oneof_uint32"), protoreflect.ValueOfUint32(8888))

		// Mutate nested message
		nested := hm.Mutable(fds.ByName("optional_nested_message")).Message()
		nested.Set(nested.Descriptor().Fields().ByName("a"), protoreflect.ValueOfInt32(9999))

		// Prepare identically mutated reference proto
		refMut := makeFullConformanceTestAllTypes()
		refMut.OptionalInt32 = 7777
		refMut.OptionalInt64 = 0
		refMut.OptionalString = "mutated string"
		refMut.RepeatedInt32[0] = 1111
		refMut.RepeatedInt32 = append(refMut.RepeatedInt32, 999)
		delete(refMut.MapStringString, "key1")
		refMut.MapStringString["key_new"] = "val_new"
		refMut.OneofField = &conformance.TestAllTypesProto3_OneofUint32{OneofUint32: 8888}
		refMut.OptionalNestedMessage.A = 9999

		// Verify wire output
		mutWire, err := proto.Marshal(hm)
		require.NoError(t, err)
		parsedWire := &conformance.TestAllTypesProto3{}
		require.NoError(t, proto.Unmarshal(mutWire, parsedWire))

		assert.Equal(t, refMut.OptionalInt32, parsedWire.OptionalInt32)
		assert.Equal(t, int64(0), parsedWire.OptionalInt64)
		assert.Equal(t, refMut.OptionalString, parsedWire.OptionalString)
		assert.Equal(t, refMut.RepeatedInt32, parsedWire.RepeatedInt32)
		assert.Equal(t, refMut.MapStringString, parsedWire.MapStringString)
		assert.Equal(t, refMut.GetOneofUint32(), parsedWire.GetOneofUint32())
		assert.Empty(t, parsedWire.GetOneofString())
		assert.Equal(t, refMut.OptionalNestedMessage.A, parsedWire.OptionalNestedMessage.A)

		// Verify JSON output
		mutJSON, err := hyperjson.Marshal(hm)
		require.NoError(t, err)
		parsedJSON := &conformance.TestAllTypesProto3{}
		require.NoError(t, protojson.Unmarshal(mutJSON, parsedJSON))

		assert.Equal(t, refMut.OptionalInt32, parsedJSON.OptionalInt32)
		assert.Equal(t, int64(0), parsedJSON.OptionalInt64)
		assert.Equal(t, refMut.OptionalString, parsedJSON.OptionalString)
		assert.Equal(t, refMut.RepeatedInt32, parsedJSON.RepeatedInt32)
		assert.Equal(t, refMut.MapStringString, parsedJSON.MapStringString)
		assert.Equal(t, refMut.GetOneofUint32(), parsedJSON.GetOneofUint32())
		assert.Empty(t, parsedJSON.GetOneofString())
		assert.Equal(t, refMut.OptionalNestedMessage.A, parsedJSON.OptionalNestedMessage.A)
	})

	t.Run("Graph_FromWireThenMutated", func(t *testing.T) {
		t.Parallel()
		orig := makeFullGraph()
		wire, err := proto.Marshal(orig)
		require.NoError(t, err)

		md := mdOf(orig)
		ct := compileFor(t, md)
		fds := md.Fields()

		hm := hyperpb.NewMessage(ct)
		require.NoError(t, hm.Unmarshal(wire))

		// Mutate root and nested submessages
		hm.Set(fds.ByName("v"), protoreflect.ValueOfInt32(100))
		subS := hm.Mutable(fds.ByName("s")).Message()
		subS.Set(fds.ByName("v"), protoreflect.ValueOfInt32(200))

		wireMut, err := proto.Marshal(hm)
		require.NoError(t, err)
		parsedWire := &testpb.Graph{}
		require.NoError(t, proto.Unmarshal(wireMut, parsedWire))
		assert.EqualValues(t, 100, parsedWire.GetV())
		assert.EqualValues(t, 200, parsedWire.GetS().GetV())

		jsonMut, err := hyperjson.Marshal(hm)
		require.NoError(t, err)
		parsedJSON := &testpb.Graph{}
		require.NoError(t, protojson.Unmarshal(jsonMut, parsedJSON))
		assert.EqualValues(t, 100, parsedJSON.GetV())
		assert.EqualValues(t, 200, parsedJSON.GetS().GetV())
	})

	t.Run("Maps_FromWireThenMutated", func(t *testing.T) {
		t.Parallel()
		orig := makeFullMaps()
		wire, err := proto.Marshal(orig)
		require.NoError(t, err)

		md := mdOf(orig)
		ct := compileFor(t, md)
		fds := md.Fields()

		hm := hyperpb.NewMessage(ct)
		require.NoError(t, hm.Unmarshal(wire))

		// Mutate map
		m10 := hm.Mutable(fds.ByName("m10")).Map()
		m10.Set(protoreflect.MapKey(protoreflect.ValueOfInt32(1)), protoreflect.ValueOfInt32(999))
		m10.Clear(protoreflect.MapKey(protoreflect.ValueOfInt32(2)))

		wireMut, err := proto.Marshal(hm)
		require.NoError(t, err)
		parsedWire := &testpb.Maps{}
		require.NoError(t, proto.Unmarshal(wireMut, parsedWire))
		assert.Equal(t, int32(999), parsedWire.GetM10()[1])
		assert.NotContains(t, parsedWire.GetM10(), int32(2))

		jsonMut, err := hyperjson.Marshal(hm)
		require.NoError(t, err)
		parsedJSON := &testpb.Maps{}
		require.NoError(t, protojson.Unmarshal(jsonMut, parsedJSON))
		assert.Equal(t, int32(999), parsedJSON.GetM10()[1])
		assert.NotContains(t, parsedJSON.GetM10(), int32(2))
	})
}

// TestKitchenSink_FullMessageFromJSONThenMutated parses a full message from JSON,
// then applies mutations across all fields and verifies wire and JSON output.
func TestKitchenSink_FullMessageFromJSONThenMutated(t *testing.T) {
	t.Parallel()

	orig := makeFullScalars()
	jsonIn, err := protojson.Marshal(orig)
	require.NoError(t, err)

	md := mdOf(orig)
	ct := compileFor(t, md)
	fds := md.Fields()

	hm := hyperpb.NewMessage(ct)
	require.NoError(t, hyperjson.Unmarshal(jsonIn, hm))

	// Mutate fields
	hm.Set(fds.ByName("a1"), protoreflect.ValueOfInt32(12345))
	hm.Set(fds.ByName("a2"), protoreflect.ValueOfInt64(0)) // proto3 zero
	hm.Clear(fds.ByName("a14"))
	hm.Set(fds.ByName("b1"), protoreflect.ValueOfInt32(0)) // optional zero with presence

	// Verify wire serialization
	wire, err := proto.Marshal(hm)
	require.NoError(t, err)
	parsedWire := mustUnmarshalScalars(t, wire)
	assert.EqualValues(t, 12345, parsedWire.GetA1())
	assert.Zero(t, parsedWire.GetA2())
	assert.Empty(t, parsedWire.GetA14())
	require.NotNil(t, parsedWire.B1)
	assert.EqualValues(t, 0, *parsedWire.B1)

	// Verify JSON serialization
	jsonOut, err := hyperjson.Marshal(hm)
	require.NoError(t, err)
	parsedJSON := &testpb.Scalars{}
	require.NoError(t, protojson.Unmarshal(jsonOut, parsedJSON))
	assert.EqualValues(t, 12345, parsedJSON.GetA1())
	assert.Zero(t, parsedJSON.GetA2())
	assert.Empty(t, parsedJSON.GetA14())
	require.NotNil(t, parsedJSON.B1)
	assert.EqualValues(t, 0, *parsedJSON.B1)
}

// TestKitchenSink_SuccessiveCycles verifies that a message instance can undergo
// multiple cycles of mutation, clearing, wire unmarshaling, and JSON unmarshaling
// without stale state leaking between cycles.
func TestKitchenSink_SuccessiveCycles(t *testing.T) {
	t.Parallel()

	md := mdOf(&testpb.Scalars{})
	ct := compileFor(t, md)
	fds := md.Fields()

	hm := hyperpb.NewMessage(ct)

	// Cycle 1: Fresh -> Mutate -> Clear(nil)
	hm.Set(fds.ByName("a1"), protoreflect.ValueOfInt32(10))
	assert.True(t, hm.Has(fds.ByName("a1")))
	hm.Clear(nil) // clear all
	assert.False(t, hm.Has(fds.ByName("a1")), "Clear(nil) should clear all fields")

	wire, err := proto.Marshal(hm)
	require.NoError(t, err)
	assert.Empty(t, wire)

	// Cycle 2: Parse wire -> Mutate -> Verify
	inputWire, err := proto.Marshal(&testpb.Scalars{A1: 20, A2: 30})
	require.NoError(t, err)
	require.NoError(t, hm.Unmarshal(inputWire))
	assert.True(t, hm.Has(fds.ByName("a1")))
	assert.True(t, hm.Has(fds.ByName("a2")))
	assert.EqualValues(t, 20, hm.Get(fds.ByName("a1")).Int())

	hm.Set(fds.ByName("a1"), protoreflect.ValueOfInt32(50))
	assert.EqualValues(t, 50, hm.Get(fds.ByName("a1")).Int())

	// Cycle 3: Reparse JSON onto same message (protobuf merge semantics) -> Mutate -> Verify
	inputJSON := []byte(`{"a1": 100, "a14": "fresh json"}`)
	require.NoError(t, hyperjson.Unmarshal(inputJSON, hm))
	assert.EqualValues(t, 100, hm.Get(fds.ByName("a1")).Int(), "a1 should be overwritten by JSON value")
	assert.Equal(t, "fresh json", hm.Get(fds.ByName("a14")).String(), "a14 should be populated from JSON")
	assert.True(t, hm.Has(fds.ByName("a2")), "previous field a2 is preserved under protobuf unmarshal merge semantics")
	assert.EqualValues(t, 30, hm.Get(fds.ByName("a2")).Int())

	hm.Set(fds.ByName("a14"), protoreflect.ValueOfString("mutated json string"))
	jsonOut, err := hyperjson.Marshal(hm)
	require.NoError(t, err)
	assert.Contains(t, string(jsonOut), `"a14":"mutated json string"`)
	assert.Contains(t, string(jsonOut), `"a2":"30"`)
}

func mustUnmarshalScalars(t testing.TB, b []byte) *testpb.Scalars {
	t.Helper()
	s := &testpb.Scalars{}
	require.NoError(t, proto.Unmarshal(b, s))
	return s
}

// Suppress unused import warnings if any.
var _ = bytes.Equal
