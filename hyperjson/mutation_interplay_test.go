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

// Interplay tests between hyperjson and hyperpb's mutation support (the
// copy-on-write overlay): mutations must be visible to hyperjson.Marshal
// (whose fast path otherwise reads arena storage directly), and proto.Marshal
// on JSON-parsed messages must never take the raw re-emit fast path (whose
// Shared.Src holds JSON, not wire).

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	"buf.build/go/hyperpb"
	"buf.build/go/hyperpb/hyperjson"
	testpb "buf.build/go/hyperpb/internal/gen/test"
)

// TestMarshalSeesMutations verifies hyperjson.Marshal reads through the
// mutation overlay instead of stale arena storage.
func TestMarshalSeesMutations(t *testing.T) {
	t.Parallel()
	md := mdOf(&testpb.Scalars{})
	ct := compileFor(t, md)

	hm := hyperpb.NewMessage(ct)
	require.NoError(t, hyperjson.Unmarshal([]byte(`{"a1": 1, "a14": "before", "a2": "7"}`), hm))

	fds := md.Fields()
	hm.Set(fds.ByNumber(14), protoreflect.ValueOfString("after"))
	hm.Set(fds.ByNumber(3), protoreflect.ValueOfUint32(99))
	hm.Clear(fds.ByNumber(1))

	out, err := hyperjson.Marshal(hm)
	require.NoError(t, err)

	check := &testpb.Scalars{}
	require.NoError(t, protojson.Unmarshal(out, check))
	assert.Equal(t, "after", check.GetA14(), "overlay write must be visible: %s", out)
	assert.EqualValues(t, 99, check.GetA3(), "overlay write must be visible: %s", out)
	assert.EqualValues(t, 7, check.GetA2(), "unmutated field must survive: %s", out)
	assert.Zero(t, check.GetA1(), "cleared field must be omitted: %s", out)
}

// TestMarshalSeesNestedMutations verifies the per-message overlay gate: a
// mutated submessage inside an unmutated parent must still marshal through
// the overlay.
func TestMarshalSeesNestedMutations(t *testing.T) {
	t.Parallel()
	md := mdOf(&testpb.Graph{})
	ct := compileFor(t, md)

	hm := hyperpb.NewMessage(ct)
	require.NoError(t, hyperjson.Unmarshal([]byte(`{"v": 1, "s": {"v": 2}}`), hm))

	fds := md.Fields()
	sub := hm.Get(fds.ByName("s")).Message()
	sub.Set(fds.ByName("v"), protoreflect.ValueOfInt32(42))

	out, err := hyperjson.Marshal(hm)
	require.NoError(t, err)

	check := &testpb.Graph{}
	require.NoError(t, protojson.Unmarshal(out, check))
	assert.EqualValues(t, 1, check.GetV())
	assert.EqualValues(t, 42, check.GetS().GetV(), "nested overlay write must be visible: %s", out)
}

// TestProtoMarshalOfJSONParsedMessage verifies that a message parsed with
// hyperjson (whose Shared.Src holds JSON input) marshals via proto.Marshal to
// valid wire bytes, rather than echoing the JSON source.
func TestProtoMarshalOfJSONParsedMessage(t *testing.T) {
	t.Parallel()
	md := mdOf(&testpb.Scalars{})
	ct := compileFor(t, md)

	hm := hyperpb.NewMessage(ct)
	require.NoError(t, hyperjson.Unmarshal([]byte(`{"a1": 42, "a14": "hello"}`), hm))

	wire, err := proto.Marshal(hm)
	require.NoError(t, err)

	check := &testpb.Scalars{}
	require.NoError(t, proto.Unmarshal(wire, check), "proto.Marshal output must be valid wire, not JSON: %x", wire)
	assert.EqualValues(t, 42, check.GetA1())
	assert.Equal(t, "hello", check.GetA14())
}

// TestProtoMarshalCanonicalizes confirms proto.Marshal re-encodes rather
// than echoing the parse input: a duplicated singular field in the input
// must collapse to one record (last wins) in the output.
func TestProtoMarshalCanonicalizes(t *testing.T) {
	t.Parallel()
	orig := &testpb.Scalars{A1: 7, A14: "x"}
	wire, err := proto.Marshal(orig)
	require.NoError(t, err)
	// Prepend a losing duplicate of a1; the parser keeps the last value.
	dup := append([]byte{0x08, 0x63}, wire...)

	hm := hyperpb.NewMessage(compileFor(t, mdOf(orig)))
	require.NoError(t, hm.Unmarshal(dup))

	out, err := proto.Marshal(hm)
	require.NoError(t, err)

	check := &testpb.Scalars{}
	require.NoError(t, proto.Unmarshal(out, check))
	assert.EqualValues(t, 7, check.GetA1())
	assert.NotEqual(t, dup, out, "output must be re-encoded, not an echo of the input")
}

// TestMutateThenJSONRoundTrip exercises the full loop: JSON in, mutate,
// JSON out, reparse.
func TestMutateThenJSONRoundTrip(t *testing.T) {
	t.Parallel()
	md := mdOf(&testpb.Repeated{})
	ct := compileFor(t, md)

	hm := hyperpb.NewMessage(ct)
	require.NoError(t, hyperjson.Unmarshal([]byte(`{"r1": [1, 2], "r7": ["a"]}`), hm))

	fds := md.Fields()
	list := hm.Mutable(fds.ByName("r1")).List()
	list.Append(protoreflect.ValueOfInt32(3))

	out, err := hyperjson.Marshal(hm)
	require.NoError(t, err)

	back := hyperpb.NewMessage(ct)
	require.NoError(t, hyperjson.Unmarshal(out, back))
	got := back.Get(fds.ByName("r1")).List()
	require.Equal(t, 3, got.Len(), "appended element must round-trip: %s", out)
	assert.EqualValues(t, 3, got.Get(2).Int())
}
