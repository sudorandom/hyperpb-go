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

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/dynamicpb"

	"buf.build/go/hyperpb"
	"buf.build/go/hyperpb/hyperjson"
	conformance "buf.build/go/hyperpb/internal/gen/conformance"
	testpb "buf.build/go/hyperpb/internal/gen/test"
)

// Differential fuzzing for hyperjson. Run over long periods with, e.g.:
//
//	./fuzz.sh            # cycles all targets until interrupted
//	go test -run xxx -fuzz '^FuzzDifferential$' -fuzztime 1h ./hyperjson/
//
// Crashing inputs are minimized and saved under testdata/fuzz/ by the Go
// fuzzing engine.

var fuzzMessages = []proto.Message{
	&testpb.Scalars{},
	&testpb.Repeated{},
	&testpb.Maps{},
	&testpb.Oneof{},
	&testpb.Graph{},
	&testpb.MessageMaps{},
	&conformance.TestAllTypesProto3{},
	&conformance.TestAllTypesProto2{},
}

var fuzzCompiled struct {
	once sync.Once
	cts  []*hyperpb.MessageType
	mds  []protoreflect.MessageDescriptor
}

func fuzzType(sel byte) (*hyperpb.MessageType, protoreflect.MessageDescriptor) {
	fuzzCompiled.once.Do(func() {
		for _, m := range fuzzMessages {
			md := m.ProtoReflect().Descriptor()
			fuzzCompiled.mds = append(fuzzCompiled.mds, md)
			fuzzCompiled.cts = append(fuzzCompiled.cts,
				hyperpb.CompileMessageDescriptor(md, hyperpb.WithExtensionsFromTypes(protoregistry.GlobalTypes)))
		}
	})
	i := int(sel) % len(fuzzMessages)
	return fuzzCompiled.cts[i], fuzzCompiled.mds[i]
}

var fuzzSeeds = []string{
	`{}`,
	`{"a1": 42, "a2": "-9223372036854775808", "a11": 1.5, "a12": "-Infinity", "a13": true, "a14": "héllo \"world\"\n", "a15": "AQIDBA=="}`,
	`{"r1": [1, -2, 3], "r7": ["a", "", "ünïcode"], "r8": ["AQ==", ""]}`,
	`{"m10": {"1": 10, "-2": 20}, "mb0": {"true": 1}, "mc0": {"": 0}, "m1d": {"3": "ENUM_2", "4": 1}}`,
	`{"s1": 5, "m8": "chosen", "tail": 9}`,
	`{"v": 1, "s": {"v": 2}, "r": [{"v": 4}, {}]}`,
	`{"optionalInt32": 1, "optionalNestedMessage": {"a": 7}, "oneofUint32": null, "oneofString": "x"}`,
	`{"optionalTimestamp": "2023-01-15T10:20:30.5Z", "optionalDuration": "-3.000000001s"}`,
	`{"optionalStruct": {"a": [1, "x", null, {"b": false}]}, "optionalValue": null}`,
	`{"optionalAny": {"@type": "type.googleapis.com/protobuf_test_messages.proto3.TestAllTypesProto3", "optionalInt32": 7}}`,
	`{"optionalBoolWrapper": false, "optionalInt64Wrapper": "9", "optionalFieldMask": "fooBar,bazQux"}`,
	`{"[protobuf_test_messages.proto2.extension_int32]": 42}`,
	`{"repeatedNullValue": [null], "optionalNullValue": null}`,
	`{"mapStringNestedMessage": {"k": {"a": 1}}, "mapInt64Int64": {"-1": "-2"}}`,
}

func fuzzSeed(f *testing.F) {
	f.Helper()
	for _, s := range fuzzSeeds {
		for sel := range fuzzMessages {
			f.Add(byte(sel), []byte(s))
		}
	}
}

// FuzzDifferential checks pure internal consistency; any failure here is a
// real bug. The direct arena writer and the JSON-to-wire transcoder must
// agree exactly (success/failure and resulting message), and every parsed
// message must survive a marshal/unmarshal round trip.
func FuzzDifferential(f *testing.F) {
	fuzzSeed(f)
	f.Fuzz(func(t *testing.T, sel byte, data []byte) {
		ct, _ := fuzzType(sel)

		direct := hyperpb.NewMessage(ct)
		errD := hyperjson.Unmarshal(data, direct)

		trans := hyperpb.NewMessage(ct)
		errT := hyperjson.TranscodeUnmarshal(data, trans)

		if (errD == nil) != (errT == nil) {
			t.Fatalf("direct/transcode error divergence:\n  direct:    %v\n  transcode: %v\n  input: %q", errD, errT, data)
		}
		if errD != nil {
			return
		}
		if !proto.Equal(direct, trans) {
			t.Fatalf("direct/transcode message divergence for input %q", data)
		}

		out, err := hyperjson.Marshal(direct)
		if err != nil {
			t.Fatalf("marshal of successfully parsed message failed: %v (input %q)", err, data)
		}
		back := hyperpb.NewMessage(ct)
		if err := hyperjson.Unmarshal(out, back); err != nil {
			t.Fatalf("round trip re-parse failed: %v\n  emitted: %q", err, out)
		}
		if !proto.Equal(direct, back) {
			t.Fatalf("round trip inequality:\n  input:   %q\n  emitted: %q", data, out)
		}
	})
}

// FuzzOracle compares against protojson as ground truth for inputs both
// parsers accept. Accept/reject divergence is expected in both directions:
// protojson leniently accepts quoted numbers with trailing garbage (e.g.
// "-2,3" parses as -2), which the conformance suite requires rejecting, and
// hyperjson accepts duplicate map keys last-wins, which protojson rejects.
func FuzzOracle(f *testing.F) {
	fuzzSeed(f)
	f.Fuzz(func(t *testing.T, sel byte, data []byte) {
		ct, md := fuzzType(sel)

		oracle := dynamicpb.NewMessage(md)
		errO := protojson.Unmarshal(data, oracle)

		hm := hyperpb.NewMessage(ct)
		errH := hyperjson.Unmarshal(data, hm)

		if errO != nil || errH != nil {
			return
		}
		if !proto.Equal(oracle, hm) {
			t.Fatalf("parse disagreement with protojson:\n  input:     %q\n  protojson: %s\n  hyperjson: %s",
				data, mustJSON(oracle), mustJSON(hm))
		}
	})
}

// FuzzWireRoundTrip feeds arbitrary wire bytes through hyperpb and compares
// hyperjson.Marshal against protojson.Marshal semantically (both reparsed
// with protojson, which drops unknown fields consistently).
func FuzzWireRoundTrip(f *testing.F) {
	for sel := range fuzzMessages {
		f.Add(byte(sel), []byte{})
		f.Add(byte(sel), []byte{0x08, 0x01})
		f.Add(byte(sel), []byte{0x72, 0x03, 'a', 'b', 'c'})
	}
	f.Fuzz(func(t *testing.T, sel byte, wire []byte) {
		ct, md := fuzzType(sel)

		hm := hyperpb.NewMessage(ct)
		if hm.Unmarshal(wire) != nil {
			return
		}

		want, errW := protojson.Marshal(hm)
		got, errG := hyperjson.Marshal(hm)
		if (errW == nil) != (errG == nil) {
			t.Fatalf("marshal error divergence:\n  protojson: %v\n  hyperjson: %v\n  wire: %x", errW, errG, wire)
		}
		if errW != nil {
			return
		}

		a := dynamicpb.NewMessage(md)
		if err := protojson.Unmarshal(want, a); err != nil {
			return // protojson emitted something it cannot itself parse.
		}
		b := dynamicpb.NewMessage(md)
		if err := protojson.Unmarshal(got, b); err != nil {
			t.Fatalf("hyperjson output is not valid protojson: %v\n  emitted: %q\n  wire: %x", err, got, wire)
		}
		if !proto.Equal(a, b) {
			t.Fatalf("marshal disagreement:\n  protojson: %s\n  hyperjson: %s\n  wire: %x", want, got, wire)
		}
	})
}

func mustJSON(m proto.Message) []byte {
	b, err := protojson.Marshal(m)
	if err != nil {
		return []byte(err.Error())
	}
	return b
}
