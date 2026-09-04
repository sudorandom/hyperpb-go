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
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/dynamicpb"

	"buf.build/go/hyperpb"
	"buf.build/go/hyperpb/hyperjson"
	testpb "buf.build/go/hyperpb/internal/gen/test"

	// Register the WKT file descriptors used by the dynamic test file.
	_ "google.golang.org/protobuf/types/known/anypb"
	_ "google.golang.org/protobuf/types/known/durationpb"
	_ "google.golang.org/protobuf/types/known/emptypb"
	_ "google.golang.org/protobuf/types/known/fieldmaskpb"
	_ "google.golang.org/protobuf/types/known/structpb"
	_ "google.golang.org/protobuf/types/known/timestamppb"
	_ "google.golang.org/protobuf/types/known/wrapperspb"
)

var (
	compiledTypes sync.Map // protoreflect.MessageDescriptor -> *hyperpb.MessageType
)

func compileFor(t testing.TB, md protoreflect.MessageDescriptor) *hyperpb.MessageType {
	t.Helper()
	if ct, ok := compiledTypes.Load(md); ok {
		return ct.(*hyperpb.MessageType)
	}
	ct := hyperpb.CompileMessageDescriptor(md)
	compiledTypes.Store(md, ct)
	return ct
}

// roundTrip drives both PoC directions from one JSON fixture:
//
//  1. protojson parses the fixture into an oracle message; the oracle's wire
//     bytes are parsed by hyperpb; hyperjson.Marshal of that message must be
//     protojson-parseable back into something proto.Equal to the oracle.
//  2. hyperjson.Unmarshal of the fixture into a fresh hyperpb message must be
//     proto.Equal to the oracle.
func roundTrip(t *testing.T, md protoreflect.MessageDescriptor, jsonIn string) {
	t.Helper()

	newDyn := func() proto.Message { return dynamicpb.NewMessage(md) }

	oracle := newDyn()
	require.NoError(t, protojson.Unmarshal([]byte(jsonIn), oracle), "oracle protojson.Unmarshal")

	wire, err := proto.Marshal(oracle)
	require.NoError(t, err)

	ct := compileFor(t, md)

	// Direction 1: marshal.
	hm := hyperpb.NewMessage(ct)
	require.NoError(t, hm.Unmarshal(wire))
	got, err := hyperjson.Marshal(hm)
	require.NoError(t, err, "hyperjson.Marshal")

	check := newDyn()
	require.NoError(t, protojson.Unmarshal(got, check), "hyperjson.Marshal output must be valid protojson: %s", got)
	assert.True(t, proto.Equal(oracle, check), "marshal mismatch:\n  hyperjson: %s\n  protojson: %s", got, mustProtojson(t, oracle))

	// Direction 2: unmarshal.
	hm2 := hyperpb.NewMessage(ct)
	require.NoError(t, hyperjson.Unmarshal([]byte(jsonIn), hm2), "hyperjson.Unmarshal")
	assert.True(t, proto.Equal(oracle, hm2), "unmarshal mismatch:\n  input: %s\n  got:   %s", jsonIn, mustProtojson(t, hm2))

	// Differential: the direct writer must be engaged for test types, and
	// the transcode path must produce an identical message.
	assert.True(t, hyperjson.IsDirect(hm2), "direct writer not engaged for %s", md.FullName())
	hmT := hyperpb.NewMessage(ct)
	require.NoError(t, hyperjson.TranscodeUnmarshal([]byte(jsonIn), hmT), "transcode path")
	assert.True(t, proto.Equal(hm2, hmT), "direct/transcode divergence:\n  direct:    %s\n  transcode: %s",
		mustProtojson(t, hm2), mustProtojson(t, hmT))

	// Direction 2b: hyperjson must also accept protojson's own output.
	pjOut := mustProtojson(t, oracle)
	hm3 := hyperpb.NewMessage(ct)
	require.NoError(t, hyperjson.Unmarshal(pjOut, hm3), "hyperjson.Unmarshal of protojson output: %s", pjOut)
	assert.True(t, proto.Equal(oracle, hm3))
}

func mustProtojson(t *testing.T, m proto.Message) []byte {
	t.Helper()
	b, err := protojson.Marshal(m)
	require.NoError(t, err)
	return b
}

func mdOf(m proto.Message) protoreflect.MessageDescriptor {
	return m.ProtoReflect().Descriptor()
}

func TestScalars(t *testing.T) {
	t.Parallel()
	md := mdOf(&testpb.Scalars{})
	roundTrip(t, md, `{}`)
	roundTrip(t, md, `{
		"a1": 42, "a2": "-9223372036854775808", "a3": 4294967295, "a4": "18446744073709551615",
		"a5": -7, "a6": "-77", "a7": 1000, "a8": "123456789012345",
		"a9": -12, "a10": "-13", "a11": 1.5, "a12": -2.25,
		"a13": true, "a14": "héllo \"world\"\n", "a15": "AQIDBA=="
	}`)
	// Explicit-presence (optional) fields, set to their zero values.
	roundTrip(t, md, `{"b1": 0, "b13": false, "b14": "", "b15": ""}`)
	// String/number flexibility and floats in strings.
	roundTrip(t, md, `{"a1": "42", "a2": 100, "a11": "1.25", "a12": "-Infinity"}`)
}

func TestScalarsNullMeansUnset(t *testing.T) {
	t.Parallel()
	md := mdOf(&testpb.Scalars{})
	ct := compileFor(t, md)
	hm := hyperpb.NewMessage(ct)
	require.NoError(t, hyperjson.Unmarshal([]byte(`{"a1": null, "b1": null, "a14": null}`), hm))
	empty := hyperpb.NewMessage(ct)
	assert.True(t, proto.Equal(empty, hm))
}

func TestFloatSpecials(t *testing.T) {
	t.Parallel()
	md := mdOf(&testpb.Scalars{})
	roundTrip(t, md, `{"a11": "NaN", "a12": "Infinity"}`)
	roundTrip(t, md, `{"a11": "-Infinity", "a12": "NaN"}`)
	roundTrip(t, md, `{"a12": 1e100}`)
}

func TestRepeated(t *testing.T) {
	t.Parallel()
	md := mdOf(&testpb.Repeated{})
	roundTrip(t, md, `{
		"r1": [1, -2, 3], "r2": ["1", "-2"], "r3": [-1, 1], "r4": ["-1", "1"],
		"r5": [0, 4294967295], "r6": ["0", "18446744073709551615"],
		"r7": ["a", "", "ünïcode"], "r8": ["AQ==", ""]
	}`)
	roundTrip(t, md, `{"r1": []}`)
}

func TestOneof(t *testing.T) {
	t.Parallel()
	md := mdOf(&testpb.Oneof{})
	roundTrip(t, md, `{"s1": 5, "m8": "chosen", "tail": 9}`)
	roundTrip(t, md, `{"m10": {"m1": 1}}`)
}

func TestOneofConflict(t *testing.T) {
	t.Parallel()
	ct := compileFor(t, mdOf(&testpb.Oneof{}))
	hm := hyperpb.NewMessage(ct)
	err := hyperjson.Unmarshal([]byte(`{"m1": 1, "m2": "2"}`), hm)
	require.ErrorContains(t, err, "oneof")
}

func TestMaps(t *testing.T) {
	t.Parallel()
	md := mdOf(&testpb.Maps{})
	roundTrip(t, md, `{
		"m10": {"1": 10, "-2": 20},
		"m20": {"-9223372036854775808": 1},
		"m30": {"4294967295": 5},
		"m40": {"18446744073709551615": 6},
		"m1d": {"3": "ENUM_2", "4": 1},
		"m1e": {"5": "five"},
		"m1f": {"6": "Bw=="},
		"mb0": {"true": 1, "false": 2},
		"mc0": {"": 0, "key": 7},
		"mce": {"a": "b"},
		"mcc": {"k": true}
	}`)
}

func TestMessageMaps(t *testing.T) {
	t.Parallel()
	md := mdOf(&testpb.MessageMaps{})
	roundTrip(t, md, `{
		"scalars": {"a1": 1},
		"mc": {"nested": {"scalars": {"a14": "deep"}, "m1": {"7": {}}}}
	}`)
}

func TestGraph(t *testing.T) {
	t.Parallel()
	md := mdOf(&testpb.Graph{})
	roundTrip(t, md, `{"v": 1, "s": {"v": 2, "s": {"v": 3}}, "r": [{"v": 4}, {}, {"v": 5, "r": [{"v": 6}]}]}`)
}

func TestUseProtoNames(t *testing.T) {
	t.Parallel()
	oracle := &testpb.Oneof{Single: &testpb.Oneof_S1{S1: 3}, Tail: 4}
	wire, err := proto.Marshal(oracle)
	require.NoError(t, err)

	hm := hyperpb.NewMessage(compileFor(t, mdOf(oracle)))
	require.NoError(t, hm.Unmarshal(wire))

	got, err := hyperjson.MarshalOptions{UseProtoNames: true}.Marshal(hm)
	require.NoError(t, err)
	want, err := protojson.MarshalOptions{UseProtoNames: true}.Marshal(oracle)
	require.NoError(t, err)
	assert.JSONEq(t, string(want), string(got))
}

func TestUnknownFields(t *testing.T) {
	t.Parallel()
	ct := compileFor(t, mdOf(&testpb.Scalars{}))

	hm := hyperpb.NewMessage(ct)
	err := hyperjson.Unmarshal([]byte(`{"nope": 1}`), hm)
	require.ErrorContains(t, err, "unknown field")

	hm = hyperpb.NewMessage(ct)
	err = hyperjson.UnmarshalOptions{DiscardUnknown: true}.Unmarshal(
		[]byte(`{"nope": {"deep": [1, {"x": null}]}, "a1": 8}`), hm)
	require.NoError(t, err)
	assert.EqualValues(t, 8, hm.Get(mdOf(&testpb.Scalars{}).Fields().ByNumber(1)).Int())
}

// TestPartialMessageReadable verifies a message left partially populated by
// a failed Unmarshal is safe to read: the direct writer must have patched the
// zero-copy source pointers even on the error path.
func TestPartialMessageReadable(t *testing.T) {
	t.Parallel()
	ct := compileFor(t, mdOf(&testpb.Scalars{}))
	hm := hyperpb.NewMessage(ct)
	err := hyperjson.Unmarshal([]byte(`{"a14": "written before the error", "a1": "bogus"}`), hm)
	require.Error(t, err)
	out, merr := protojson.Marshal(hm)
	require.NoError(t, merr)
	assert.Contains(t, string(out), "written before the error")
}

func TestUnmarshalErrors(t *testing.T) {
	t.Parallel()
	ct := compileFor(t, mdOf(&testpb.Scalars{}))
	for name, input := range map[string]string{
		"duplicate field":    `{"a1": 1, "a1": 2}`,
		"dup via json name":  `{"a1": 1, "a1": 1}`,
		"trailing data":      `{} {}`,
		"bad number":         `{"a1": 12x}`,
		"number overflow":    `{"a1": 2147483648}`,
		"uint negative":      `{"a3": -1}`,
		"bool string":        `{"a13": "true"}`,
		"bad base64":         `{"a15": "@@@"}`,
		"float overflow":     `{"a11": 1e100}`,
		"bare value":         `4`,
		"unterminated":       `{"a14": "x`,
		"bad utf8 escape":    `{"a14": "\udc00"}`,
		"control in string":  "{\"a14\": \"\x01\"}",
		"array for scalar":   `{"a1": [1]}`,
		"number for string":  `{"a14": 3}`,
		"object for int":     `{"a1": {}}`,
		"non-integral float": `{"a1": 1.5}`,
		"leading zero":       `{"a1": 01}`,
		"neg leading zero":   `{"a1": -01}`,
	} {
		t.Run(name, func(t *testing.T) {
			hm := hyperpb.NewMessage(ct)
			assert.Error(t, hyperjson.Unmarshal([]byte(input), hm), "input: %s", input)
		})
	}
}

// buildWKTFile synthesizes a descriptor with one field per well-known type,
// since the checked-in test protos don't use them.
func buildWKTFile(t *testing.T) protoreflect.MessageDescriptor {
	t.Helper()

	field := func(name string, num int32, typ descriptorpb.FieldDescriptorProto_Type, typeName string, label descriptorpb.FieldDescriptorProto_Label) *descriptorpb.FieldDescriptorProto {
		f := &descriptorpb.FieldDescriptorProto{
			Name:   proto.String(name),
			Number: proto.Int32(num),
			Type:   typ.Enum(),
			Label:  label.Enum(),
		}
		if typeName != "" {
			f.TypeName = proto.String(typeName)
		}
		return f
	}
	msg := func(name, typeName string, num int32) *descriptorpb.FieldDescriptorProto {
		return field(name, num, descriptorpb.FieldDescriptorProto_TYPE_MESSAGE, typeName, descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL)
	}

	fdp := &descriptorpb.FileDescriptorProto{
		Name:    proto.String("hyperjson/wkt_test.proto"),
		Package: proto.String("hyperjson.test"),
		Syntax:  proto.String("proto3"),
		Dependency: []string{
			"google/protobuf/any.proto",
			"google/protobuf/duration.proto",
			"google/protobuf/empty.proto",
			"google/protobuf/field_mask.proto",
			"google/protobuf/struct.proto",
			"google/protobuf/timestamp.proto",
			"google/protobuf/wrappers.proto",
			"test/test.proto",
		},
		MessageType: []*descriptorpb.DescriptorProto{{
			Name: proto.String("WKT"),
			Field: []*descriptorpb.FieldDescriptorProto{
				msg("ts", ".google.protobuf.Timestamp", 1),
				msg("dur", ".google.protobuf.Duration", 2),
				msg("wb", ".google.protobuf.BoolValue", 3),
				msg("wi32", ".google.protobuf.Int32Value", 4),
				msg("wi64", ".google.protobuf.Int64Value", 5),
				msg("wu32", ".google.protobuf.UInt32Value", 6),
				msg("wu64", ".google.protobuf.UInt64Value", 7),
				msg("wf", ".google.protobuf.FloatValue", 8),
				msg("wd", ".google.protobuf.DoubleValue", 9),
				msg("ws", ".google.protobuf.StringValue", 10),
				msg("wby", ".google.protobuf.BytesValue", 11),
				msg("st", ".google.protobuf.Struct", 12),
				msg("val", ".google.protobuf.Value", 13),
				msg("lv", ".google.protobuf.ListValue", 14),
				msg("fm", ".google.protobuf.FieldMask", 15),
				msg("any", ".google.protobuf.Any", 16),
				msg("empty", ".google.protobuf.Empty", 17),
				field("nv", 18, descriptorpb.FieldDescriptorProto_TYPE_ENUM, ".google.protobuf.NullValue", descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL),
				field("vals", 19, descriptorpb.FieldDescriptorProto_TYPE_MESSAGE, ".google.protobuf.Value", descriptorpb.FieldDescriptorProto_LABEL_REPEATED),
				msg("scalars", ".hyperpb.test.Scalars", 20),
			},
		}},
	}

	fd, err := protodesc.NewFile(fdp, protoregistry.GlobalFiles)
	require.NoError(t, err)
	return fd.Messages().Get(0)
}

func TestWellKnownTypes(t *testing.T) {
	t.Parallel()
	md := buildWKTFile(t)

	roundTrip(t, md, `{}`)
	roundTrip(t, md, `{"ts": "2023-01-15T10:20:30.5Z", "dur": "-3.000000001s"}`)
	roundTrip(t, md, `{"ts": "0001-01-01T00:00:00Z", "dur": "0s"}`)
	roundTrip(t, md, `{"ts": "9999-12-31T23:59:59.999999999Z"}`)
	roundTrip(t, md, `{"ts": "2023-01-15T12:00:00+05:30"}`)
	roundTrip(t, md, `{
		"wb": true, "wi32": -1, "wi64": "2", "wu32": 3, "wu64": "4",
		"wf": 1.5, "wd": -0.25, "ws": "wrapped", "wby": "AA=="
	}`)
	roundTrip(t, md, `{"wb": false, "wi32": 0, "ws": ""}`)
	roundTrip(t, md, `{
		"st": {"a": 1, "b": "two", "c": [true, null, {"d": []}], "e": {}},
		"val": {"nested": [1, "x"]},
		"lv": [1, [2, [3]]],
		"vals": [null, false, "s", 1.5]
	}`)
	roundTrip(t, md, `{"val": null}`)
	roundTrip(t, md, `{"nv": null}`)
	roundTrip(t, md, `{"fm": "fooBar,bazQux.subPath"}`)
	roundTrip(t, md, `{"fm": ""}`)
	roundTrip(t, md, `{"empty": {}}`)
	roundTrip(t, md, `{"any": {}}`)
	roundTrip(t, md, `{"any": {"@type": "type.googleapis.com/hyperpb.test.Scalars", "a1": 7, "a14": "in any"}}`)
	roundTrip(t, md, `{"any": {"a14": "type last", "@type": "type.googleapis.com/hyperpb.test.Scalars"}}`)
	roundTrip(t, md, `{"any": {"@type": "type.googleapis.com/google.protobuf.Duration", "value": "1.5s"}}`)
	roundTrip(t, md, `{"scalars": {"a1": 1}}`)
}

func TestGoldenOutput(t *testing.T) {
	t.Parallel()
	md := buildWKTFile(t)
	ct := compileFor(t, md)

	for input, want := range map[string]string{
		`{"ts": "2023-01-15T10:20:30.500Z"}`:  `{"ts":"2023-01-15T10:20:30.500Z"}`,
		`{"ts": "2023-01-15T10:20:30Z"}`:      `{"ts":"2023-01-15T10:20:30Z"}`,
		`{"ts": "2023-01-15T12:00:00+05:30"}`: `{"ts":"2023-01-15T06:30:00Z"}`,
		`{"dur": "-3.000000001s"}`:            `{"dur":"-3.000000001s"}`,
		`{"dur": "1.500s"}`:                   `{"dur":"1.500s"}`,
		`{"wi64": "77"}`:                      `{"wi64":"77"}`,
		`{"wf": "NaN"}`:                       `{"wf":"NaN"}`,
		`{"val": null}`:                       `{"val":null}`,
		`{"fm": "aB,cD"}`:                     `{"fm":"aB,cD"}`,
	} {
		hm := hyperpb.NewMessage(ct)
		require.NoError(t, hyperjson.Unmarshal([]byte(input), hm), "input: %s", input)
		got, err := hyperjson.Marshal(hm)
		require.NoError(t, err)
		assert.Equal(t, want, string(got), "input: %s", input)
	}
}

func TestWKTErrors(t *testing.T) {
	t.Parallel()
	md := buildWKTFile(t)
	ct := compileFor(t, md)

	for name, input := range map[string]string{
		"timestamp out of range": `{"ts": "10000-01-01T00:00:00Z"}`,
		"timestamp garbage":      `{"ts": "yesterday"}`,
		"duration no suffix":     `{"dur": "3"}`,
		"duration overflow":      `{"dur": "999999999999999s"}`,
		"fieldmask snake":        `{"fm": "foo_bar"}`,
		"any missing type":       `{"any": {"a1": 1}}`,
		"any unknown type":       `{"any": {"@type": "type.googleapis.com/no.such.Type"}}`,
		"empty with field":       `{"empty": {"x": 1}}`,
	} {
		t.Run(name, func(t *testing.T) {
			hm := hyperpb.NewMessage(ct)
			assert.Error(t, hyperjson.Unmarshal([]byte(input), hm), "input: %s", input)
		})
	}
}
