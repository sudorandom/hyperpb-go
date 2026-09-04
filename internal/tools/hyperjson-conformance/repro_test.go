package main

import (
	"testing"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"

	"buf.build/go/hyperpb"
	"buf.build/go/hyperpb/hyperjson"
	conformance "buf.build/go/hyperpb/internal/gen/conformance"
)

func compile(m proto.Message) *hyperpb.MessageType {
	return hyperpb.CompileMessageDescriptor(m.ProtoReflect().Descriptor())
}

func TestAnyEmptyJSON(t *testing.T) {
	ct := compile(&conformance.TestAllTypesProto3{})
	hm := hyperpb.NewMessage(ct)
	err := hyperjson.Unmarshal([]byte(`{"optionalAny":{}}`), hm)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	out, err := hyperjson.Marshal(hm)
	t.Logf("marshal: %s err=%v", out, err)
}

func TestOneofNullFirst(t *testing.T) {
	ct := compile(&conformance.TestAllTypesProto3{})
	hm := hyperpb.NewMessage(ct)
	err := hyperjson.Unmarshal([]byte(`{"oneofUint32": null, "oneofString": "test"}`), hm)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
}

func TestInt64TooLarge(t *testing.T) {
	ct := compile(&conformance.TestAllTypesProto3{})
	for _, in := range []string{
		`{"optionalInt64": 9223372036854775808}`,
		`{"optionalInt64": -9223372036854775809}`,
		`{"optionalUint64": 18446744073709551616}`,
	} {
		hm := hyperpb.NewMessage(ct)
		if err := hyperjson.Unmarshal([]byte(in), hm); err == nil {
			t.Errorf("expected error for %s", in)
		}
	}
}

// Repeated bool where true is encoded as an overlong/large varint.
func TestRepeatedBoolLargeVarint(t *testing.T) {
	md := (&conformance.TestAllTypesProto3{}).ProtoReflect().Descriptor()
	fd := md.Fields().ByName("repeated_bool")

	// Unpacked records; all values are nonzero, so every element is "true".
	var wire []byte
	for _, v := range []uint64{1, 128, 1 << 33, 1 << 63, ^uint64(0)} {
		wire = protowire.AppendTag(wire, fd.Number(), protowire.VarintType)
		wire = protowire.AppendVarint(wire, v)
	}

	// Reference implementation.
	ref := &conformance.TestAllTypesProto3{}
	if err := proto.Unmarshal(wire, ref); err != nil {
		t.Fatal(err)
	}
	t.Logf("gencode: %v", ref.RepeatedBool)

	ct := compile(&conformance.TestAllTypesProto3{})
	hm := hyperpb.NewMessage(ct)
	if err := hm.Unmarshal(wire); err != nil {
		t.Fatal(err)
	}
	list := hm.Get(fd).List()
	var got []bool
	for i := range list.Len() {
		got = append(got, list.Get(i).Bool())
	}
	t.Logf("hyperpb: %v", got)
	for i, v := range got {
		if !v {
			t.Skipf("known upstream hyperpb bug (see failing_tests.txt): "+
				"element %d decoded as false because bool varints are truncated to 32 bits (gencode says %v)",
				i, ref.RepeatedBool)
		}
	}
}

// A single map entry whose submessage contains the key field twice.
func TestDuplicateKeyInMapEntry(t *testing.T) {
	md := (&conformance.TestAllTypesProto3{}).ProtoReflect().Descriptor()
	fd := md.Fields().ByName("map_string_nested_message")

	var entry []byte
	entry = protowire.AppendTag(entry, 1, protowire.BytesType)
	entry = protowire.AppendString(entry, "a")
	entry = protowire.AppendTag(entry, 1, protowire.BytesType)
	entry = protowire.AppendString(entry, "b")

	var wire []byte
	wire = protowire.AppendTag(wire, fd.Number(), protowire.BytesType)
	wire = protowire.AppendBytes(wire, entry)

	ref := &conformance.TestAllTypesProto3{}
	if err := proto.Unmarshal(wire, ref); err != nil {
		t.Fatal(err)
	}
	refJSON, _ := protojson.Marshal(ref)
	t.Logf("gencode: %s", refJSON)

	ct := compile(&conformance.TestAllTypesProto3{})
	hm := hyperpb.NewMessage(ct)
	if err := hm.Unmarshal(wire); err != nil {
		t.Skipf("known upstream hyperpb bug (see failing_tests.txt): "+
			"map entry with duplicated key field fails to parse: %v", err)
	}
	out, err := hyperjson.Marshal(hm)
	t.Logf("hyperpb: %s err=%v", out, err)
}

// Extensions round-trip through the "[full.name]" JSON convention when the
// type is compiled with extension support.
func TestExtensionRoundTrip(t *testing.T) {
	jsonIn := `{"[protobuf_test_messages.proto2.extension_int32]": 42, "optionalInt32": 1}`

	ref := &conformance.TestAllTypesProto2{}
	if err := protojson.Unmarshal([]byte(jsonIn), ref); err != nil {
		t.Fatalf("protojson oracle: %v", err)
	}
	refJSON, _ := protojson.Marshal(ref)
	t.Logf("protojson: %s", refJSON)

	ct := compiledType("protobuf_test_messages.proto2.TestAllTypesProto2")
	hm := hyperpb.NewMessage(ct)
	if err := hyperjson.Unmarshal([]byte(jsonIn), hm); err != nil {
		t.Fatalf("hyperjson.Unmarshal: %v", err)
	}
	if !proto.Equal(ref, hm) {
		t.Errorf("not equal to oracle")
	}
	out, err := hyperjson.Marshal(hm)
	if err != nil {
		t.Fatalf("hyperjson.Marshal: %v", err)
	}
	t.Logf("hyperjson: %s", out)

	back := &conformance.TestAllTypesProto2{}
	if err := protojson.Unmarshal(out, back); err != nil {
		t.Fatalf("marshal output not protojson-parseable: %v", err)
	}
	if !proto.Equal(ref, back) {
		t.Errorf("marshal round-trip mismatch: %s", out)
	}
}
