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

func TestMarshal_ProtocGenGoStruct(t *testing.T) {
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

	gotWire, err := hyperpb.Marshal(orig)
	require.NoError(t, err)

	wantWire, err := proto.Marshal(orig)
	require.NoError(t, err)

	assert.Equal(t, wantWire, gotWire)

	// Test MarshalAppend
	prefix := []byte("header")
	appended, err := hyperpb.MarshalAppend(prefix, orig)
	require.NoError(t, err)
	assert.Equal(t, append(prefix, wantWire...), appended)
}

func TestMarshal_CompileMarshal(t *testing.T) {
	t.Parallel()

	orig := &testpb.Graph{
		V: 1,
		S: &testpb.Graph{
			V: 2,
		},
	}

	enc, err := hyperpb.CompileMarshal[*testpb.Graph]()
	require.NoError(t, err)

	gotWire, err := enc.Marshal(orig)
	require.NoError(t, err)

	wantWire, err := proto.Marshal(orig)
	require.NoError(t, err)

	assert.Equal(t, wantWire, gotWire)
}

func TestMarshal_Nil(t *testing.T) {
	t.Parallel()

	wire, err := hyperpb.Marshal(nil)
	require.NoError(t, err)
	assert.Nil(t, wire)
}
