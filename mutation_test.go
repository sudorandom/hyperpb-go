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

package hyperpb_test

import (
	"testing"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/dynamicpb"

	"buf.build/go/hyperpb"
)

func TestMutationBasic(t *testing.T) {
	t.Parallel()

	mt, err := protoregistry.GlobalTypes.FindMessageByName("hyperpb.test.Graph")
	if err != nil {
		t.Fatalf("failed to find hyperpb.test.Graph: %v", err)
	}

	fastType := hyperpb.CompileMessageDescriptor(mt.Descriptor())
	msg := hyperpb.NewMessage(fastType)

	// Test singular scalar mutation
	fdV := mt.Descriptor().Fields().ByName("v")
	if msg.Has(fdV) {
		t.Error("expected v to not be populated initially")
	}
	msg.Set(fdV, protoreflect.ValueOfInt32(42))
	if !msg.Has(fdV) {
		t.Error("expected v to be populated after Set")
	}
	if msg.Get(fdV).Int() != 42 {
		t.Errorf("expected v to be 42, got %v", msg.Get(fdV).Interface())
	}

	// Test singular message mutation
	fdS := mt.Descriptor().Fields().ByName("s")
	if msg.Has(fdS) {
		t.Error("expected s to not be populated initially")
	}
	sVal := msg.Mutable(fdS)
	if !msg.Has(fdS) {
		t.Error("expected s to be populated after Mutable")
	}
	sMsg, ok := sVal.Message().Interface().(*hyperpb.Message)
	if !ok {
		t.Fatal("expected *hyperpb.Message")
	}
	sMsg.Set(fdV, protoreflect.ValueOfInt32(100))

	// Verify nested reads
	nestedMsg, ok := msg.Get(fdS).Message().Interface().(*hyperpb.Message)
	if !ok {
		t.Fatal("expected *hyperpb.Message")
	}
	if nestedMsg.Get(fdV).Int() != 100 {
		t.Error("expected nested field value to be 100")
	}

	// Test list mutation
	fdR := mt.Descriptor().Fields().ByName("r")
	if msg.Has(fdR) {
		t.Error("expected r to not be populated initially")
	}
	rVal := msg.Mutable(fdR)
	rList := rVal.List()
	newElem := rList.AppendMutable()
	rMsg, ok := newElem.Message().Interface().(*hyperpb.Message)
	if !ok {
		t.Fatal("expected *hyperpb.Message")
	}
	rMsg.Set(fdV, protoreflect.ValueOfInt32(200))
	if !msg.Has(fdR) {
		t.Error("expected r to be populated after element is appended")
	}
	if rList.Len() != 1 {
		t.Errorf("expected list len 1, got %d", rList.Len())
	}

	// Marshal and verify
	data, err := proto.Marshal(msg)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	// Unmarshal using gencode message
	gencodeMsg := mt.New().Interface()
	if err := proto.Unmarshal(data, gencodeMsg); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	// Verify values on the gencode message
	vGencode := gencodeMsg.ProtoReflect().Get(fdV).Int()
	if vGencode != 42 {
		t.Errorf("gencode: expected v to be 42, got %d", vGencode)
	}

	sGencode := gencodeMsg.ProtoReflect().Get(fdS).Message()
	if sGencode.Get(fdV).Int() != 100 {
		t.Errorf("gencode: expected nested v to be 100, got %d", sGencode.Get(fdV).Int())
	}

	rGencode := gencodeMsg.ProtoReflect().Get(fdR).List()
	if rGencode.Len() != 1 {
		t.Errorf("gencode: expected repeated len 1, got %d", rGencode.Len())
	}
	if rGencode.Get(0).Message().Get(fdV).Int() != 200 {
		t.Errorf("gencode: expected repeated element v to be 200, got %d", rGencode.Get(0).Message().Get(fdV).Int())
	}
}

func TestMutationGencodeNested(t *testing.T) {
	t.Parallel()

	mt, err := protoregistry.GlobalTypes.FindMessageByName("hyperpb.test.Graph")
	if err != nil {
		t.Fatalf("failed to find hyperpb.test.Graph: %v", err)
	}

	fastType := hyperpb.CompileMessageDescriptor(mt.Descriptor())
	msg := hyperpb.NewMessage(fastType)

	fdS := mt.Descriptor().Fields().ByName("s")
	fdV := mt.Descriptor().Fields().ByName("v")

	// Create a standard gencode message instance
	gencodeMsg := mt.New()
	gencodeMsg.Set(fdV, protoreflect.ValueOfInt32(999))

	// Set the gencode message as a submessage of the hyperpb message
	msg.Set(fdS, protoreflect.ValueOfMessage(gencodeMsg))

	// Attempt to marshal the hyperpb message containing the nested gencode message
	_, err = proto.Marshal(msg)
	if err != nil {
		t.Fatalf("failed to marshal message with nested gencode: %v", err)
	}
}

func TestMutationMaps(t *testing.T) {
	t.Parallel()

	mt, err := protoregistry.GlobalTypes.FindMessageByName("hyperpb.test.Maps")
	if err != nil {
		t.Fatalf("failed to find hyperpb.test.Maps: %v", err)
	}

	fastType := hyperpb.CompileMessageDescriptor(mt.Descriptor())
	msg := hyperpb.NewMessage(fastType)

	fdM10 := mt.Descriptor().Fields().ByName("m10") // map<int32, int32>
	fdM1E := mt.Descriptor().Fields().ByName("m1e") // map<int32, string>

	// Mutate maps
	m10Val := msg.Mutable(fdM10)
	m10Map := m10Val.Map()
	m10Map.Set(protoreflect.MapKey(protoreflect.ValueOfInt32(1)), protoreflect.ValueOfInt32(10))
	m10Map.Set(protoreflect.MapKey(protoreflect.ValueOfInt32(2)), protoreflect.ValueOfInt32(20))

	m1eVal := msg.Mutable(fdM1E)
	m1eMap := m1eVal.Map()
	m1eMap.Set(protoreflect.MapKey(protoreflect.ValueOfInt32(100)), protoreflect.ValueOfString("hello"))

	// Check Has, Get, and Len
	if !msg.Has(fdM10) {
		t.Error("expected m10 map to be populated")
	}
	if m10Map.Len() != 2 {
		t.Errorf("expected m10 map len 2, got %d", m10Map.Len())
	}
	if m10Map.Get(protoreflect.MapKey(protoreflect.ValueOfInt32(2))).Int() != 20 {
		t.Error("expected m10[2] to be 20")
	}

	if !m1eMap.Has(protoreflect.MapKey(protoreflect.ValueOfInt32(100))) {
		t.Error("expected m1e map to have key 100")
	}

	// Delete map entry
	m10Map.Clear(protoreflect.MapKey(protoreflect.ValueOfInt32(1)))
	if m10Map.Len() != 1 {
		t.Errorf("expected m10 map len 1 after clear, got %d", m10Map.Len())
	}

	// Marshal and verify
	data, err := proto.Marshal(msg)
	if err != nil {
		t.Fatalf("failed to marshal map: %v", err)
	}

	gencodeMsg := mt.New().Interface()
	if err := proto.Unmarshal(data, gencodeMsg); err != nil {
		t.Fatalf("failed to unmarshal map: %v", err)
	}

	gM10 := gencodeMsg.ProtoReflect().Get(fdM10).Map()
	if gM10.Len() != 1 {
		t.Errorf("gencode expected map len 1, got %d", gM10.Len())
	}
	if gM10.Get(protoreflect.MapKey(protoreflect.ValueOfInt32(2))).Int() != 20 {
		t.Error("gencode expected m10[2] to be 20")
	}

	gM1E := gencodeMsg.ProtoReflect().Get(fdM1E).Map()
	if gM1E.Get(protoreflect.MapKey(protoreflect.ValueOfInt32(100))).String() != "hello" {
		t.Error("gencode expected m1e[100] to be hello")
	}
}

func TestMutationOneof(t *testing.T) {
	t.Parallel()

	mt, err := protoregistry.GlobalTypes.FindMessageByName("hyperpb.test.Oneof")
	if err != nil {
		t.Fatalf("failed to find hyperpb.test.Oneof: %v", err)
	}

	fastType := hyperpb.CompileMessageDescriptor(mt.Descriptor())
	msg := hyperpb.NewMessage(fastType)

	odMulti := mt.Descriptor().Oneofs().ByName("multi")
	fdM1 := mt.Descriptor().Fields().ByName("m1") // int32
	fdM8 := mt.Descriptor().Fields().ByName("m8") // string

	if active := msg.WhichOneof(odMulti); active != nil {
		t.Errorf("expected no active oneof field initially, got %v", active.Name())
	}

	// Set one oneof field
	msg.Set(fdM1, protoreflect.ValueOfInt32(1234))
	if active := msg.WhichOneof(odMulti); active != fdM1 {
		t.Errorf("expected active oneof field to be m1, got %v", active)
	}
	if !msg.Has(fdM1) {
		t.Error("expected m1 to be populated")
	}

	// Set another oneof field (should clear the first one)
	msg.Set(fdM8, protoreflect.ValueOfString("hello"))
	if active := msg.WhichOneof(odMulti); active != fdM8 {
		t.Errorf("expected active oneof field to be m8, got %v", active)
	}
	if msg.Has(fdM1) {
		t.Error("expected m1 to be cleared automatically after setting m8")
	}

	// Clear the oneof active field
	msg.Clear(fdM8)
	if active := msg.WhichOneof(odMulti); active != nil {
		t.Errorf("expected active oneof to be nil after clear, got %v", active)
	}

	// Set again and marshal
	msg.Set(fdM1, protoreflect.ValueOfInt32(5678))
	data, err := proto.Marshal(msg)
	if err != nil {
		t.Fatalf("failed to marshal oneof: %v", err)
	}

	gencodeMsg := mt.New().Interface()
	if err := proto.Unmarshal(data, gencodeMsg); err != nil {
		t.Fatalf("failed to unmarshal oneof: %v", err)
	}

	gMulti := gencodeMsg.ProtoReflect().WhichOneof(odMulti)
	if gMulti != fdM1 {
		t.Errorf("gencode expected active oneof m1, got %v", gMulti)
	}
	if gencodeMsg.ProtoReflect().Get(fdM1).Int() != 5678 {
		t.Errorf("gencode expected m1 = 5678, got %v", gencodeMsg.ProtoReflect().Get(fdM1).Interface())
	}
}

func TestMutationCoW(t *testing.T) {
	t.Parallel()

	mt, err := protoregistry.GlobalTypes.FindMessageByName("hyperpb.test.Graph")
	if err != nil {
		t.Fatalf("failed to find hyperpb.test.Graph: %v", err)
	}

	fdV := mt.Descriptor().Fields().ByName("v")
	fdS := mt.Descriptor().Fields().ByName("s")

	// Create original message Graph { v: 4, s: { v: 100 } }
	gencodeMsg := mt.New().Interface()
	gencodeMsg.ProtoReflect().Set(fdV, protoreflect.ValueOfInt32(4))
	sVal := gencodeMsg.ProtoReflect().Mutable(fdS)
	sVal.Message().Set(fdV, protoreflect.ValueOfInt32(100))

	data, err := proto.Marshal(gencodeMsg)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	fastType := hyperpb.CompileMessageDescriptor(mt.Descriptor())
	ctx := new(hyperpb.Shared)
	msgOriginal := ctx.NewMessage(fastType)
	if err := proto.Unmarshal(data, msgOriginal); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	// Now perform mutation on msgOriginal
	sMutableVal := msgOriginal.Mutable(fdS)
	sMutableMsg, ok := sMutableVal.Message().Interface().(*hyperpb.Message)
	if !ok {
		t.Fatal("expected *hyperpb.Message")
	}
	sMutableMsg.Set(fdV, protoreflect.ValueOfInt32(500))

	// Verify msgOriginal has s.v = 500
	nestedMsg, ok := msgOriginal.Get(fdS).Message().Interface().(*hyperpb.Message)
	if !ok {
		t.Fatal("expected *hyperpb.Message")
	}
	if nestedMsg.Get(fdV).Int() != 500 {
		t.Error("expected mutated msg to have nested v = 500")
	}

	// Verify that the fallback / original parsed memory does NOT have it changed.
	msgSecond := new(hyperpb.Shared).NewMessage(fastType)
	if err := proto.Unmarshal(data, msgSecond); err != nil {
		t.Fatalf("failed to unmarshal second: %v", err)
	}

	nestedMsgSecond, ok := msgSecond.Get(fdS).Message().Interface().(*hyperpb.Message)
	if !ok {
		t.Fatal("expected *hyperpb.Message")
	}
	if nestedMsgSecond.Get(fdV).Int() != 100 {
		t.Error("expected unmodified parsed message to keep original nested v = 100")
	}
}

func TestMutationNestedParsedMessageDisablesRawMarshal(t *testing.T) {
	t.Parallel()

	mt, err := protoregistry.GlobalTypes.FindMessageByName("hyperpb.test.Graph")
	if err != nil {
		t.Fatalf("failed to find hyperpb.test.Graph: %v", err)
	}

	fdV := mt.Descriptor().Fields().ByName("v")
	fdS := mt.Descriptor().Fields().ByName("s")

	gencodeMsg := mt.New().Interface()
	gencodeMsg.ProtoReflect().Set(fdV, protoreflect.ValueOfInt32(4))
	gencodeMsg.ProtoReflect().Mutable(fdS).Message().Set(fdV, protoreflect.ValueOfInt32(100))

	data, err := proto.Marshal(gencodeMsg)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	fastType := hyperpb.CompileMessageDescriptor(mt.Descriptor())
	msg := new(hyperpb.Shared).NewMessage(fastType)
	if err := proto.Unmarshal(data, msg); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	nested, ok := msg.Get(fdS).Message().Interface().(*hyperpb.Message)
	if !ok {
		t.Fatal("expected *hyperpb.Message")
	}
	nested.Set(fdV, protoreflect.ValueOfInt32(500))

	gotData, err := proto.Marshal(msg)
	if err != nil {
		t.Fatalf("failed to marshal mutated message: %v", err)
	}

	got := mt.New().Interface()
	if err := proto.Unmarshal(gotData, got); err != nil {
		t.Fatalf("failed to unmarshal mutated output: %v", err)
	}
	if got.ProtoReflect().Get(fdS).Message().Get(fdV).Int() != 500 {
		t.Fatalf("expected nested mutation to be marshaled, got %d", got.ProtoReflect().Get(fdS).Message().Get(fdV).Int())
	}
}

func TestMutationClear(t *testing.T) {
	t.Parallel()

	mt, err := protoregistry.GlobalTypes.FindMessageByName("hyperpb.test.Graph")
	if err != nil {
		t.Fatalf("failed to find hyperpb.test.Graph: %v", err)
	}

	fdV := mt.Descriptor().Fields().ByName("v")
	fdS := mt.Descriptor().Fields().ByName("s")

	fastType := hyperpb.CompileMessageDescriptor(mt.Descriptor())
	msg := hyperpb.NewMessage(fastType)

	msg.Set(fdV, protoreflect.ValueOfInt32(42))
	sVal := msg.Mutable(fdS)
	sMsg, ok := sVal.Message().Interface().(*hyperpb.Message)
	if !ok {
		t.Fatal("expected *hyperpb.Message")
	}
	sMsg.Set(fdV, protoreflect.ValueOfInt32(100))

	// Clear individual fields
	msg.Clear(fdV)
	if msg.Has(fdV) {
		t.Error("expected v to be cleared")
	}
	if msg.Get(fdV).Int() != 0 {
		t.Error("expected cleared v to return default value 0")
	}

	// Entire message reset
	msg.Reset()
	if msg.Has(fdS) {
		t.Error("expected s to be cleared after Reset")
	}
}

func BenchmarkMarshal(b *testing.B) {
	mt, err := protoregistry.GlobalTypes.FindMessageByName("hyperpb.test.Graph")
	if err != nil {
		b.Fatalf("failed to find hyperpb.test.Graph: %v", err)
	}

	fdV := mt.Descriptor().Fields().ByName("v")
	fdS := mt.Descriptor().Fields().ByName("s")

	// 1. Create a typical payload
	gencodeMsg := mt.New().Interface()
	gencodeMsg.ProtoReflect().Set(fdV, protoreflect.ValueOfInt32(42))
	sVal := gencodeMsg.ProtoReflect().Mutable(fdS)
	sVal.Message().Set(fdV, protoreflect.ValueOfInt32(100))

	// Marshal to bytes
	data, err := proto.Marshal(gencodeMsg)
	if err != nil {
		b.Fatalf("failed to marshal: %v", err)
	}

	fastType := hyperpb.CompileMessageDescriptor(mt.Descriptor())

	// Benchmark Gencode Message serialization
	b.Run("gencode", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			_, err := proto.Marshal(gencodeMsg)
			if err != nil {
				b.Fatal(err)
			}
		}
	})

	// Benchmark hyperpb unmutated message serialization (direct fallback from arena)
	b.Run("hyperpb-unmutated", func(b *testing.B) {
		b.ReportAllocs()
		msgOriginal := new(hyperpb.Shared).NewMessage(fastType)
		if err := proto.Unmarshal(data, msgOriginal); err != nil {
			b.Fatal(err)
		}
		b.ResetTimer()
		for range b.N {
			_, err := proto.Marshal(msgOriginal)
			if err != nil {
				b.Fatal(err)
			}
		}
	})

	// Benchmark dynamicpb message serialization
	b.Run("dynamicpb", func(b *testing.B) {
		b.ReportAllocs()
		dynMsg := dynamicpb.NewMessage(mt.Descriptor())
		if err := proto.Unmarshal(data, dynMsg); err != nil {
			b.Fatal(err)
		}
		b.ResetTimer()
		for range b.N {
			_, err := proto.Marshal(dynMsg)
			if err != nil {
				b.Fatal(err)
			}
		}
	})

	// Benchmark hyperpb mutated message serialization (from overlay)
	b.Run("hyperpb-mutated", func(b *testing.B) {
		b.ReportAllocs()
		msgMutated := new(hyperpb.Shared).NewMessage(fastType)
		// Set fields directly on overlay
		msgMutated.Set(fdV, protoreflect.ValueOfInt32(42))
		sValMut := msgMutated.Mutable(fdS)
		sMsg, ok := sValMut.Message().Interface().(*hyperpb.Message)
		if !ok {
			b.Fatal("expected *hyperpb.Message")
		}
		sMsg.Set(fdV, protoreflect.ValueOfInt32(100))
		b.ResetTimer()
		for range b.N {
			_, err := proto.Marshal(msgMutated)
			if err != nil {
				b.Fatal(err)
			}
		}
	})
}
