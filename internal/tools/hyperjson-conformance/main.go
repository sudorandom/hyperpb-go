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

// hyperjson-conformance is a testee for the official protobuf conformance
// test runner. It parses payloads with hyperpb and serves the JSON side with
// hyperjson, exercising both directions of the codec:
//
//   - protobuf payload -> hyperpb wire parse -> hyperjson.Marshal
//   - JSON payload     -> hyperjson.Unmarshal (JSON->wire transcode ->
//     hyperpb wire parse) -> hyperjson.Marshal or generic proto.Marshal
//
// Run it with the conformance_test_runner from protocolbuffers/protobuf, or
// the pure-JS reimplementation shipped in the protobuf-conformance npm
// package (no bazel required):
//
//	npm install protobuf-conformance
//	npx conformance_test_runner --enforce_recommended --maximum_edition PROTO3 \
//	    --failure_list failing_tests.txt ./hyperjson-conformance
//
// failing_tests.txt lists the tests expected to fail due to known hyperpb
// wire-parser bugs (not hyperjson bugs); see the comments in that file and
// the repros in repro_test.go. Text-format tests are skipped, as are the
// editions test messages (only the proto2/proto3 descriptors are linked in).
package main

import (
	"errors"

	"encoding/binary"
	"fmt"
	"io"
	"os"
	"sync"

	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"

	"buf.build/go/hyperpb"
	"buf.build/go/hyperpb/hyperjson"
	conformance "buf.build/go/hyperpb/internal/gen/conformance"

	// The suite's Any tests reference google.protobuf.Empty by type URL; link
	// it so the registry can resolve it.
	_ "google.golang.org/protobuf/types/known/emptypb"
)

func main() {
	var sizeBuf [4]byte
	inbuf := make([]byte, 0, 4096)
	for {
		if _, err := io.ReadFull(os.Stdin, sizeBuf[:]); err == io.EOF {
			return
		} else if err != nil {
			fatalf("read request: %v", err)
		}

		size := binary.LittleEndian.Uint32(sizeBuf[:])
		if int(size) > cap(inbuf) {
			inbuf = make([]byte, size)
		}
		inbuf = inbuf[:size]
		if _, err := io.ReadFull(os.Stdin, inbuf); err != nil {
			fatalf("read request: %v", err)
		}

		req := &conformance.ConformanceRequest{}
		if err := proto.Unmarshal(inbuf, req); err != nil {
			fatalf("parse request: %v", err)
		}

		out, err := proto.Marshal(handle(req))
		if err != nil {
			fatalf("marshal response: %v", err)
		}
		binary.LittleEndian.PutUint32(sizeBuf[:], uint32(len(out)))
		if _, err := os.Stdout.Write(sizeBuf[:]); err != nil {
			fatalf("write response: %v", err)
		}
		if _, err := os.Stdout.Write(out); err != nil {
			fatalf("write response: %v", err)
		}
	}
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "hyperjson-conformance: "+format+"\n", args...)
	os.Exit(1)
}

var (
	typeCache sync.Map // protoreflect.FullName -> *hyperpb.MessageType
)

// compiledType resolves a conformance message type name to a compiled
// hyperpb type. Only the proto2/proto3 test messages linked into this binary
// resolve; editions types are reported as skipped.
func compiledType(name string) *hyperpb.MessageType {
	if ct, ok := typeCache.Load(protoreflect.FullName(name)); ok {
		return ct.(*hyperpb.MessageType) //nolint:errcheck
	}
	desc, err := protoregistry.GlobalFiles.FindDescriptorByName(protoreflect.FullName(name))
	if err != nil {
		return nil
	}
	md, ok := desc.(protoreflect.MessageDescriptor)
	if !ok {
		return nil
	}
	// Compile with extension support so proto2 extension fields become real
	// fields instead of unknown-field chunks.
	ct := hyperpb.CompileMessageDescriptor(md, hyperpb.WithExtensionsFromTypes(protoregistry.GlobalTypes))
	typeCache.Store(protoreflect.FullName(name), ct)
	return ct
}

func handle(req *conformance.ConformanceRequest) *conformance.ConformanceResponse {
	if os.Getenv("HYPERJSON_CONFORMANCE_DEBUG") != "" {
		fmt.Fprintf(os.Stderr, "req: type=%q payload=%T\n", req.GetMessageType(), req.GetPayload())
	}
	ct := compiledType(req.GetMessageType())
	if ct == nil {
		return skipped("unsupported message type: " + req.GetMessageType())
	}

	msg := hyperpb.NewMessage(ct)
	switch payload := req.GetPayload().(type) {
	case *conformance.ConformanceRequest_ProtobufPayload:
		// The conformance suite requires rejecting overlong tag varints,
		// which hyperpb (like protobuf-go) accepts.
		if err := validateStrictWire(payload.ProtobufPayload); err != nil {
			return parseError(err)
		}
		if err := msg.Unmarshal(payload.ProtobufPayload); err != nil {
			return parseError(err)
		}
	case *conformance.ConformanceRequest_JsonPayload:
		opts := hyperjson.UnmarshalOptions{
			DiscardUnknown: req.GetTestCategory() == conformance.TestCategory_JSON_IGNORE_UNKNOWN_PARSING_TEST,
		}
		if err := opts.Unmarshal([]byte(payload.JsonPayload), msg); err != nil {
			return parseError(err)
		}
	case *conformance.ConformanceRequest_TextPayload:
		return skipped("text format is outside hyperjson scope")
	default:
		return &conformance.ConformanceResponse{
			Result: &conformance.ConformanceResponse_RuntimeError{RuntimeError: "unknown request payload type"},
		}
	}

	switch req.GetRequestedOutputFormat() {
	case conformance.WireFormat_PROTOBUF:
		// hyperpb has no wire serializer; generic reflection-based
		// proto.Marshal works on hyperpb messages.
		out, err := proto.Marshal(msg)
		if err != nil {
			return serializeError(err)
		}
		return &conformance.ConformanceResponse{
			Result: &conformance.ConformanceResponse_ProtobufPayload{ProtobufPayload: out},
		}
	case conformance.WireFormat_JSON:
		out, err := hyperjson.Marshal(msg)
		if err != nil {
			return serializeError(err)
		}
		return &conformance.ConformanceResponse{
			Result: &conformance.ConformanceResponse_JsonPayload{JsonPayload: string(out)},
		}
	case conformance.WireFormat_TEXT_FORMAT:
		return skipped("text format is outside hyperjson scope")
	default:
		return skipped("unsupported output format")
	}
}

func parseError(err error) *conformance.ConformanceResponse {
	return &conformance.ConformanceResponse{
		Result: &conformance.ConformanceResponse_ParseError{ParseError: err.Error()},
	}
}

func serializeError(err error) *conformance.ConformanceResponse {
	return &conformance.ConformanceResponse{
		Result: &conformance.ConformanceResponse_SerializeError{SerializeError: err.Error()},
	}
}

func skipped(msg string) *conformance.ConformanceResponse {
	return &conformance.ConformanceResponse{
		Result: &conformance.ConformanceResponse_Skipped{Skipped: msg},
	}
}

// validateStrictWire rejects wire encodings the conformance suite requires a
// parser to reject but that hyperpb tolerates: overlong tag varints and
// invalid wire types.
func validateStrictWire(data []byte) error {
	for len(data) > 0 {
		tag, n := protowire.ConsumeVarint(data)
		if n < 0 {
			return protowire.ParseError(n)
		}
		if protowire.SizeVarint(tag) != n {
			return errors.New("overlong varint field tag")
		}
		num, typ := protowire.DecodeTag(tag)
		if num < protowire.MinValidNumber || num > protowire.MaxValidNumber {
			return errors.New("invalid field number")
		}
		if typ < protowire.VarintType || typ > protowire.Fixed32Type {
			return errors.New("invalid wire type")
		}
		m := protowire.ConsumeFieldValue(num, typ, data[n:])
		if m < 0 {
			return protowire.ParseError(m)
		}
		data = data[n+m:]
	}
	return nil
}
