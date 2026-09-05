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
	_ "google.golang.org/protobuf/types/known/anypb"
	_ "google.golang.org/protobuf/types/known/durationpb"
	_ "google.golang.org/protobuf/types/known/emptypb"
	_ "google.golang.org/protobuf/types/known/fieldmaskpb"
	"google.golang.org/protobuf/types/known/structpb"
	_ "google.golang.org/protobuf/types/known/timestamppb"
	_ "google.golang.org/protobuf/types/known/wrapperspb"

	"buf.build/go/hyperpb"
	"buf.build/go/hyperpb/hyperjson"
	conformance "buf.build/go/hyperpb/internal/gen/conformance"
	testpb "buf.build/go/hyperpb/internal/gen/test"
)

var compiledTypes sync.Map // protoreflect.MessageDescriptor -> *hyperpb.MessageType

func compileFor(t testing.TB, md protoreflect.MessageDescriptor) *hyperpb.MessageType {
	t.Helper()
	if ct, ok := compiledTypes.Load(md); ok {
		return ct.(*hyperpb.MessageType) //nolint:errcheck
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

	gotAppend, err := hyperjson.MarshalAppend(nil, hm)
	require.NoError(t, err, "hyperjson.MarshalAppend")
	assert.Equal(t, got, gotAppend, "Marshal and MarshalAppend output must match")

	prefix := []byte("prefix:")
	gotPrefix, err := hyperjson.MarshalAppend(prefix, hm)
	require.NoError(t, err, "hyperjson.MarshalAppend with prefix")
	assert.Equal(t, string(append(prefix, got...)), string(gotPrefix))

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
			t.Parallel()
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
			t.Parallel()
			hm := hyperpb.NewMessage(ct)
			assert.Error(t, hyperjson.Unmarshal([]byte(input), hm), "input: %s", input)
		})
	}
}

func TestNilMessageAPI(t *testing.T) {
	t.Parallel()
	var nilMsg *hyperpb.Message

	_, err := hyperjson.Marshal(nilMsg)
	require.Error(t, err)

	_, err = hyperjson.MarshalAppend([]byte{}, nilMsg)
	require.Error(t, err)

	_, err = hyperjson.MarshalOptions{}.Marshal(nilMsg)
	require.Error(t, err)

	_, err = hyperjson.MarshalOptions{}.MarshalAppend([]byte{}, nilMsg)
	require.Error(t, err)

	err = hyperjson.Unmarshal([]byte("{}"), nilMsg)
	require.Error(t, err)

	err = hyperjson.UnmarshalOptions{}.Unmarshal([]byte("{}"), nilMsg)
	require.Error(t, err)
}

func TestDurationBoundaryCases(t *testing.T) {
	t.Parallel()
	md := buildWKTFile(t)
	ct := compileFor(t, md)

	// Valid max and min duration boundaries
	for _, in := range []string{
		`{"dur": "315576000000s"}`,
		`{"dur": "-315576000000s"}`,
		`{"dur": "315576000000.999999999s"}`,
		`{"dur": "-315576000000.999999999s"}`,
		`{"dur": "0s"}`,
	} {
		hm := hyperpb.NewMessage(ct)
		require.NoError(t, hyperjson.Unmarshal([]byte(in), hm), "input: %s", in)
		out, err := hyperjson.Marshal(hm)
		require.NoError(t, err)
		if in != `{"dur": "0s"}` {
			assert.Contains(t, string(out), "315576000000")
		}
	}

	// Invalid durations strictly beyond boundaries
	for _, in := range []string{
		`{"dur": "315576000001s"}`,
		`{"dur": "-315576000001s"}`,
		`{"dur": "315576000001.000000001s"}`,
		`{"dur": "-315576000001.000000001s"}`,
		`{"dur": "315576000000.1000000000s"}`, // 10 fractional digits
	} {
		hm := hyperpb.NewMessage(ct)
		assert.Error(t, hyperjson.Unmarshal([]byte(in), hm), "expected error for: %s", in)
	}
}

type testCustomResolver struct {
	called bool
}

func (r *testCustomResolver) FindMessageByURL(url string) (protoreflect.MessageType, error) {
	return protoregistry.GlobalTypes.FindMessageByURL(url)
}

func (r *testCustomResolver) FindExtensionByName(name protoreflect.FullName) (protoreflect.ExtensionType, error) {
	r.called = true
	return protoregistry.GlobalTypes.FindExtensionByName(name)
}

func TestCustomExtensionResolver(t *testing.T) {
	t.Parallel()
	md := mdOf(&testpb.Extensions{})
	ct := compileFor(t, md)

	res := &testCustomResolver{}
	opts := hyperjson.UnmarshalOptions{Resolver: res}
	hm := hyperpb.NewMessage(ct)

	err := opts.Unmarshal([]byte(`{"[hyperpb.test.b1]": 42}`), hm)
	require.NoError(t, err)
	assert.True(t, res.called, "custom ExtensionResolver should have been called")
}

func TestMarshalUseEnumNumbers(t *testing.T) {
	t.Parallel()
	md := buildWKTFile(t)
	ct := compileFor(t, md)
	hm := hyperpb.NewMessage(ct)

	// Field 18 is NullValue enum
	fdNV := md.Fields().ByName("nv")
	hm.Set(fdNV, protoreflect.ValueOfEnum(0))

	// NullValue should emit null in both modes (even with UseEnumNumbers: true)
	outName, err := hyperjson.MarshalOptions{UseEnumNumbers: false, EmitDefaultValues: true}.Marshal(hm)
	require.NoError(t, err)
	assert.Contains(t, string(outName), `"nv":null`)

	outNum, err := hyperjson.MarshalOptions{UseEnumNumbers: true, EmitDefaultValues: true}.Marshal(hm)
	require.NoError(t, err)
	assert.Contains(t, string(outNum), `"nv":null`)

	// Normal enum in a proto message map
	enMd := mdOf(&testpb.Maps{})
	enCt := compileFor(t, enMd)
	enHm := hyperpb.NewMessage(enCt)
	m1dFd := enMd.Fields().ByName("m1d")
	mp := enHm.Mutable(m1dFd).Map()
	mp.Set(protoreflect.ValueOfInt32(42).MapKey(), protoreflect.ValueOfEnum(1)) // testpb.Enum_ENUM_1

	outEnName, err := hyperjson.MarshalOptions{UseEnumNumbers: false}.Marshal(enHm)
	require.NoError(t, err)
	assert.Contains(t, string(outEnName), `"42":"ENUM_1"`)

	outEnNum, err := hyperjson.MarshalOptions{UseEnumNumbers: true}.Marshal(enHm)
	require.NoError(t, err)
	assert.Contains(t, string(outEnNum), `"42":1`)

	// Normal singular enum and oneof NullValue in TestAllTypesProto3
	cProto := &conformance.TestAllTypesProto3{
		OptionalNestedEnum: conformance.TestAllTypesProto3_BAR,
		OneofField:         &conformance.TestAllTypesProto3_OneofNullValue{OneofNullValue: structpb.NullValue_NULL_VALUE},
	}
	cWire, err := proto.Marshal(cProto)
	require.NoError(t, err)
	cHm := hyperpb.NewMessage(compileFor(t, mdOf(cProto)))
	require.NoError(t, cHm.Unmarshal(cWire))

	cName, err := hyperjson.MarshalOptions{UseEnumNumbers: false}.Marshal(cHm)
	require.NoError(t, err)
	assert.Contains(t, string(cName), `"optionalNestedEnum":"BAR"`)
	assert.Contains(t, string(cName), `"oneofNullValue":null`)

	cNum, err := hyperjson.MarshalOptions{UseEnumNumbers: true}.Marshal(cHm)
	require.NoError(t, err)
	assert.Contains(t, string(cNum), `"optionalNestedEnum":1`)
	assert.Contains(t, string(cNum), `"oneofNullValue":null`)
}

func TestMarshalMultilineIndent(t *testing.T) {
	t.Parallel()
	gProto := &testpb.Graph{V: 1, S: &testpb.Graph{V: 2}, R: []*testpb.Graph{{V: 3}}}
	gWire, err := proto.Marshal(gProto)
	require.NoError(t, err)

	gCt := compileFor(t, mdOf(gProto))
	gHm := hyperpb.NewMessage(gCt)
	require.NoError(t, gHm.Unmarshal(gWire))

	// Invalid indent
	_, err = hyperjson.MarshalOptions{Indent: " invalid"}.Marshal(gHm)
	require.Error(t, err)

	// Default 2 spaces
	outDef, err := hyperjson.MarshalOptions{Multiline: true}.Marshal(gHm)
	require.NoError(t, err)
	expectedDef, err := protojson.MarshalOptions{Multiline: true}.Marshal(gProto)
	require.NoError(t, err)
	assert.Equal(t, string(expectedDef), string(outDef))

	// Tab indent
	outTab, err := hyperjson.MarshalOptions{Indent: "\t"}.Marshal(gHm)
	require.NoError(t, err)
	expectedTab, err := protojson.MarshalOptions{Indent: "\t"}.Marshal(gProto)
	require.NoError(t, err)
	assert.Equal(t, string(expectedTab), string(outTab))

	// Empty message multiline
	emptyProto := &testpb.Graph{}
	emptyHm := hyperpb.NewMessage(gCt)
	outEmpty, err := hyperjson.MarshalOptions{Multiline: true}.Marshal(emptyHm)
	require.NoError(t, err)
	expectedEmpty, err := protojson.MarshalOptions{Multiline: true}.Marshal(emptyProto)
	require.NoError(t, err)
	assert.Equal(t, string(expectedEmpty), string(outEmpty))
}

func TestMarshalEmitDefaultValues(t *testing.T) {
	t.Parallel()

	// 1. Scalars
	scProto := &testpb.Scalars{}
	scCt := compileFor(t, mdOf(scProto))
	scHm := hyperpb.NewMessage(scCt)
	outSc, err := hyperjson.MarshalOptions{EmitDefaultValues: true}.Marshal(scHm)
	require.NoError(t, err)
	expSc, err := protojson.MarshalOptions{EmitDefaultValues: true}.Marshal(scProto)
	require.NoError(t, err)
	assert.JSONEq(t, string(expSc), string(outSc))

	// 2. Graph (message field omitted, repeated field emitted as [])
	gProto := &testpb.Graph{}
	gCt := compileFor(t, mdOf(gProto))
	gHm := hyperpb.NewMessage(gCt)
	outG, err := hyperjson.MarshalOptions{EmitDefaultValues: true}.Marshal(gHm)
	require.NoError(t, err)
	expG, err := protojson.MarshalOptions{EmitDefaultValues: true}.Marshal(gProto)
	require.NoError(t, err)
	assert.JSONEq(t, string(expG), string(outG))

	// 3. Repeated (all [] emitted)
	rProto := &testpb.Repeated{}
	rCt := compileFor(t, mdOf(rProto))
	rHm := hyperpb.NewMessage(rCt)
	outR, err := hyperjson.MarshalOptions{EmitDefaultValues: true}.Marshal(rHm)
	require.NoError(t, err)
	expR, err := protojson.MarshalOptions{EmitDefaultValues: true}.Marshal(rProto)
	require.NoError(t, err)
	assert.JSONEq(t, string(expR), string(outR))

	// 4. Maps (all {} emitted)
	mProto := &testpb.Maps{}
	mCt := compileFor(t, mdOf(mProto))
	mHm := hyperpb.NewMessage(mCt)
	outM, err := hyperjson.MarshalOptions{EmitDefaultValues: true}.Marshal(mHm)
	require.NoError(t, err)
	expM, err := protojson.MarshalOptions{EmitDefaultValues: true}.Marshal(mProto)
	require.NoError(t, err)
	assert.JSONEq(t, string(expM), string(outM))

	// 5. Oneof (unpopulated oneofs omitted)
	oProto := &testpb.Oneof{}
	oCt := compileFor(t, mdOf(oProto))
	oHm := hyperpb.NewMessage(oCt)
	outO, err := hyperjson.MarshalOptions{EmitDefaultValues: true}.Marshal(oHm)
	require.NoError(t, err)
	expO, err := protojson.MarshalOptions{EmitDefaultValues: true}.Marshal(oProto)
	require.NoError(t, err)
	assert.JSONEq(t, string(expO), string(outO))
}

func TestMarshalEmitUnpopulated(t *testing.T) {
	t.Parallel()

	// 1. Scalars
	scProto := &testpb.Scalars{}
	scCt := compileFor(t, mdOf(scProto))
	scHm := hyperpb.NewMessage(scCt)
	outSc, err := hyperjson.MarshalOptions{EmitUnpopulated: true}.Marshal(scHm)
	require.NoError(t, err)
	expSc, err := protojson.MarshalOptions{EmitUnpopulated: true}.Marshal(scProto)
	require.NoError(t, err)
	assert.JSONEq(t, string(expSc), string(outSc))

	// 2. Graph (message field emitted as null, repeated as [])
	gProto := &testpb.Graph{}
	gCt := compileFor(t, mdOf(gProto))
	gHm := hyperpb.NewMessage(gCt)
	outG, err := hyperjson.MarshalOptions{EmitUnpopulated: true}.Marshal(gHm)
	require.NoError(t, err)
	expG, err := protojson.MarshalOptions{EmitUnpopulated: true}.Marshal(gProto)
	require.NoError(t, err)
	assert.JSONEq(t, string(expG), string(outG))

	// 3. Repeated (all [] emitted)
	rProto := &testpb.Repeated{}
	rCt := compileFor(t, mdOf(rProto))
	rHm := hyperpb.NewMessage(rCt)
	outR, err := hyperjson.MarshalOptions{EmitUnpopulated: true}.Marshal(rHm)
	require.NoError(t, err)
	expR, err := protojson.MarshalOptions{EmitUnpopulated: true}.Marshal(rProto)
	require.NoError(t, err)
	assert.JSONEq(t, string(expR), string(outR))

	// 4. Maps (all {} emitted)
	mProto := &testpb.Maps{}
	mCt := compileFor(t, mdOf(mProto))
	mHm := hyperpb.NewMessage(mCt)
	outM, err := hyperjson.MarshalOptions{EmitUnpopulated: true}.Marshal(mHm)
	require.NoError(t, err)
	expM, err := protojson.MarshalOptions{EmitUnpopulated: true}.Marshal(mProto)
	require.NoError(t, err)
	assert.JSONEq(t, string(expM), string(outM))

	// 5. Oneof (unpopulated oneofs omitted)
	oProto := &testpb.Oneof{}
	oCt := compileFor(t, mdOf(oProto))
	oHm := hyperpb.NewMessage(oCt)
	outO, err := hyperjson.MarshalOptions{EmitUnpopulated: true}.Marshal(oHm)
	require.NoError(t, err)
	expO, err := protojson.MarshalOptions{EmitUnpopulated: true}.Marshal(oProto)
	require.NoError(t, err)
	assert.JSONEq(t, string(expO), string(outO))
}

func TestProtoMessageMarshal(t *testing.T) {
	t.Parallel()

	// 1. Populated Scalars
	sc := &testpb.Scalars{
		A1:  42,
		A2:  1234567890123,
		A13: true,
		A14: "hello world",
		A15: []byte("bytes data"),
		B1:  proto.Int32(99),
	}
	gotSc, err := hyperjson.Marshal(sc)
	require.NoError(t, err)
	wantSc, err := protojson.Marshal(sc)
	require.NoError(t, err)
	assert.JSONEq(t, string(wantSc), string(gotSc))

	// MarshalAppend
	prefix := []byte("sc:")
	gotApp, err := hyperjson.MarshalAppend(prefix, sc)
	require.NoError(t, err)
	assert.Equal(t, string(append(prefix, gotSc...)), string(gotApp))

	// 2. Recursive Graph
	g := &testpb.Graph{
		V: 1,
		S: &testpb.Graph{V: 2, S: &testpb.Graph{V: 3}},
		R: []*testpb.Graph{{V: 10}, {V: 20}},
	}
	gotG, err := hyperjson.Marshal(g)
	require.NoError(t, err)
	wantG, err := protojson.Marshal(g)
	require.NoError(t, err)
	assert.JSONEq(t, string(wantG), string(gotG))

	// Multiline on proto.Message
	gotGMulti, err := hyperjson.MarshalOptions{Multiline: true}.Marshal(g)
	require.NoError(t, err)
	wantGMulti, err := protojson.MarshalOptions{Multiline: true}.Marshal(g)
	require.NoError(t, err)
	assert.Equal(t, string(wantGMulti), string(gotGMulti))

	// 3. Conformance message with enums
	c := &conformance.TestAllTypesProto3{
		OptionalInt32:      100,
		OptionalNestedEnum: conformance.TestAllTypesProto3_BAR,
		OneofField:         &conformance.TestAllTypesProto3_OneofNullValue{OneofNullValue: structpb.NullValue_NULL_VALUE},
	}
	gotName, err := hyperjson.MarshalOptions{UseEnumNumbers: false}.Marshal(c)
	require.NoError(t, err)
	assert.Contains(t, string(gotName), `"optionalNestedEnum":"BAR"`)
	assert.Contains(t, string(gotName), `"oneofNullValue":null`)

	gotNum, err := hyperjson.MarshalOptions{UseEnumNumbers: true}.Marshal(c)
	require.NoError(t, err)
	assert.Contains(t, string(gotNum), `"optionalNestedEnum":1`)
	assert.Contains(t, string(gotNum), `"oneofNullValue":null`)
}

func TestProtoMessageUnmarshal(t *testing.T) {
	t.Parallel()

	// 1. Scalars
	jsonIn := `{"a1": 42, "a2": "1234567890123", "a13": true, "a14": "hello", "a15": "Ynl0ZXM=", "b1": 99}`
	oracleSc := &testpb.Scalars{}
	require.NoError(t, protojson.Unmarshal([]byte(jsonIn), oracleSc))

	gotSc := &testpb.Scalars{}
	require.NoError(t, hyperjson.Unmarshal([]byte(jsonIn), gotSc))
	assert.True(t, proto.Equal(oracleSc, gotSc), "expected Equal:\n  oracle: %v\n  got:    %v", oracleSc, gotSc)

	// 2. Graph
	jsonG := `{"v": 1, "s": {"v": 2, "s": {"v": 3}}, "r": [{"v": 10}, {"v": 20}]}`
	oracleG := &testpb.Graph{}
	require.NoError(t, protojson.Unmarshal([]byte(jsonG), oracleG))

	gotG := &testpb.Graph{}
	require.NoError(t, hyperjson.Unmarshal([]byte(jsonG), gotG))
	assert.True(t, proto.Equal(oracleG, gotG), "expected Equal:\n  oracle: %v\n  got:    %v", oracleG, gotG)

	// 3. Conformance with enums and discard unknown
	jsonConf := `{"optionalInt32": 100, "optionalNestedEnum": "BAR", "oneofNullValue": null, "unknownExtra": 123}`
	oracleConf := &conformance.TestAllTypesProto3{}
	require.NoError(t, protojson.UnmarshalOptions{DiscardUnknown: true}.Unmarshal([]byte(jsonConf), oracleConf))

	gotConf := &conformance.TestAllTypesProto3{}
	require.NoError(t, hyperjson.UnmarshalOptions{DiscardUnknown: true}.Unmarshal([]byte(jsonConf), gotConf))
	assert.True(t, proto.Equal(oracleConf, gotConf), "expected Equal:\n  oracle: %v\n  got:    %v", oracleConf, gotConf)

	// Reject unknown without DiscardUnknown
	err := hyperjson.Unmarshal([]byte(jsonConf), gotConf)
	assert.Error(t, err)
}

func TestNilProtoMessage(t *testing.T) {
	t.Parallel()

	var nilProto *testpb.Scalars
	_, err := hyperjson.Marshal(nilProto)
	require.Error(t, err)

	_, err = hyperjson.MarshalAppend(nil, nilProto)
	require.Error(t, err)

	err = hyperjson.Unmarshal([]byte("{}"), nilProto)
	require.Error(t, err)

	_, err = hyperjson.Marshal(nil)
	require.Error(t, err)

	err = hyperjson.Unmarshal([]byte("{}"), nil)
	require.Error(t, err)
}

func TestAllowPartial(t *testing.T) {
	t.Parallel()

	// 1. Standard proto.Message with required fields
	req := &testpb.Required{}

	// Marshal without AllowPartial -> error
	_, err := hyperjson.Marshal(req)
	require.Error(t, err)

	// Marshal with AllowPartial -> succeeds
	jsonBytes, err := hyperjson.MarshalOptions{AllowPartial: true}.Marshal(req)
	require.NoError(t, err)
	assert.JSONEq(t, "{}", string(jsonBytes))

	// Unmarshal without AllowPartial -> error
	got := &testpb.Required{}
	err = hyperjson.Unmarshal([]byte("{}"), got)
	require.Error(t, err)

	// Unmarshal with AllowPartial -> succeeds
	gotPartial := &testpb.Required{}
	err = hyperjson.UnmarshalOptions{AllowPartial: true}.Unmarshal([]byte("{}"), gotPartial)
	require.NoError(t, err)

	// 2. hyperpb.Message with required fields
	md := (&testpb.Required{}).ProtoReflect().Descriptor()
	ct := hyperpb.CompileMessageDescriptor(md)

	hm := hyperpb.NewMessage(ct)
	_, err = hyperjson.Marshal(hm)
	require.Error(t, err)

	jsonHM, err := hyperjson.MarshalOptions{AllowPartial: true}.Marshal(hm)
	require.NoError(t, err)
	assert.JSONEq(t, "{}", string(jsonHM))

	hmGot := hyperpb.NewMessage(ct)
	err = hyperjson.Unmarshal([]byte("{}"), hmGot)
	require.Error(t, err)

	hmGotPartial := hyperpb.NewMessage(ct)
	err = hyperjson.UnmarshalOptions{AllowPartial: true}.Unmarshal([]byte("{}"), hmGotPartial)
	require.NoError(t, err)
}
