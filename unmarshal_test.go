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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	"buf.build/go/hyperpb"
	testpb "buf.build/go/hyperpb/internal/gen/test"
)

func TestUniversalUnmarshal(t *testing.T) {
	t.Parallel()

	// 1. Standard protoc-gen-go struct
	orig := &testpb.Scalars{
		A1:  42,
		A14: "hello hyperpb",
		A15: []byte("bytes data"),
	}
	wire, err := proto.Marshal(orig)
	require.NoError(t, err)

	got := &testpb.Scalars{}
	err = hyperpb.Unmarshal(wire, got)
	require.NoError(t, err)
	assert.True(t, proto.Equal(orig, got))

	// 2. CompileStruct
	dec, err := hyperpb.CompileStruct[*testpb.Scalars]()
	require.NoError(t, err)

	gotCompiled := &testpb.Scalars{}
	err = dec.Unmarshal(wire, gotCompiled)
	require.NoError(t, err)
	assert.True(t, proto.Equal(orig, gotCompiled))

	// 3. hyperpb.Message
	ht := hyperpb.CompileMessageDescriptor(orig.ProtoReflect().Descriptor())
	hm := hyperpb.NewMessage(ht)
	err = hyperpb.Unmarshal(wire, hm)
	require.NoError(t, err)
	assert.Equal(t, int32(42), int32(hm.Get(orig.ProtoReflect().Descriptor().Fields().ByName("a1")).Int()))

	// 4. Nil message
	err = hyperpb.Unmarshal(wire, nil)
	assert.Error(t, err)
}
