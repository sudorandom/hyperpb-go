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

package bench

import (
	stdjson "encoding/json"
	"testing"

	jsonv2 "github.com/go-json-experiment/json"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/dynamicpb"

	"buf.build/go/hyperpb"
	"buf.build/go/hyperpb/hyperjson"
	testpb "buf.build/go/hyperpb/internal/gen/test"
)

// Plain Go struct equivalents of each benchmark shape, following protojson's
// document conventions: 64-bit integers as JSON strings, bytes as base64.
// These are what a Go service would define to handle the same documents
// without protobuf.

type goScalars struct {
	A1  int32   `json:"a1,omitempty"`
	A2  int64   `json:"a2,omitempty,string"`
	A3  uint32  `json:"a3,omitempty"`
	A4  uint64  `json:"a4,omitempty,string"`
	A5  int32   `json:"a5,omitempty"`
	A6  int64   `json:"a6,omitempty,string"`
	A7  uint32  `json:"a7,omitempty"`
	A8  uint64  `json:"a8,omitempty,string"`
	A9  int32   `json:"a9,omitempty"`
	A10 int64   `json:"a10,omitempty,string"`
	A11 float32 `json:"a11,omitempty"`
	A12 float64 `json:"a12,omitempty"`
	A13 bool    `json:"a13,omitempty"`
	A14 string  `json:"a14,omitempty"`
	A15 []byte  `json:"a15,omitempty"`
}

type goGraph struct {
	V int32      `json:"v,omitempty"`
	S *goGraph   `json:"s,omitempty"`
	R []*goGraph `json:"r,omitempty"`
}

type goMaps struct {
	M10 map[int32]int32   `json:"m10,omitempty"`
	MC0 map[string]int32  `json:"mc0,omitempty"`
	MCE map[string]string `json:"mce,omitempty"`
	M1D map[int32]string  `json:"m1d,omitempty"`
}

type goRepeated struct {
	R1 []int32  `json:"r1,omitempty"`
	R2 []string `json:"r2,omitempty"`
	R7 []string `json:"r7,omitempty"`
	R8 [][]byte `json:"r8,omitempty"`
}

// benchCases are protojson documents used to seed every implementation.
var benchCases = []struct {
	name  string
	msg   proto.Message
	newGo func() any
	json  string
}{
	{
		name:  "scalars",
		msg:   &testpb.Scalars{},
		newGo: func() any { return new(goScalars) },
		json: `{
			"a1": 42, "a2": "-9223372036854775808", "a3": 4294967295,
			"a4": "18446744073709551615", "a5": -7, "a6": "-77", "a7": 1000,
			"a8": "123456789012345", "a9": -12, "a10": "-13", "a11": 1.5,
			"a12": -2.25, "a13": true,
			"a14": "a modestly sized string value", "a15": "AQIDBAUGBwgJCg=="
		}`,
	},
	{
		name:  "graph",
		msg:   &testpb.Graph{},
		newGo: func() any { return new(goGraph) },
		json: `{"v": 1, "s": {"v": 2, "s": {"v": 3, "r": [{"v": 31}, {"v": 32}]}},
			"r": [{"v": 4}, {"v": 5, "s": {"v": 51}}, {"v": 6}, {"v": 7},
			      {"v": 8, "r": [{"v": 81}, {"v": 82}, {"v": 83}]}]}`,
	},
	{
		name:  "maps",
		msg:   &testpb.Maps{},
		newGo: func() any { return new(goMaps) },
		json: `{
			"m10": {"1": 10, "2": 20, "3": 30, "4": 40},
			"mc0": {"alpha": 1, "beta": 2, "gamma": 3},
			"mce": {"k1": "v1", "k2": "v2", "k3": "v3", "k4": "v4"},
			"m1d": {"1": "ENUM_1", "2": "ENUM_2"}
		}`,
	},
	{
		name:  "repeated",
		msg:   &testpb.Repeated{},
		newGo: func() any { return new(goRepeated) },
		json: `{
			"r1": [1, 2, 3, 4, 5, 6, 7, 8, 9, 10, -1, -2, -3, -4, -5],
			"r2": ["100", "200", "300", "400", "500"],
			"r7": ["one", "two", "three", "four", "five", "six"],
			"r8": ["AQ==", "Ag==", "Aw=="]
		}`,
	},
}

// benchSetup holds every representation of one benchmark case: the gencode
// oracle, its wire bytes, its canonical (whitespace-free) protojson document,
// a parsed hyperpb message, and a populated plain Go struct.
type benchSetup struct {
	oracle    proto.Message
	wire      []byte
	canonical []byte
	ct        *hyperpb.MessageType
	hm        *hyperpb.Message
	goValue   any
}

func setup(b *testing.B, msg proto.Message, jsonIn string, newGo func() any) *benchSetup {
	b.Helper()
	s := &benchSetup{oracle: proto.Clone(msg)}

	if err := protojson.Unmarshal([]byte(jsonIn), s.oracle); err != nil {
		b.Fatal(err)
	}
	var err error
	if s.wire, err = proto.Marshal(s.oracle); err != nil {
		b.Fatal(err)
	}
	// Canonical document: every unmarshal benchmark parses these same bytes.
	if s.canonical, err = protojson.Marshal(s.oracle); err != nil {
		b.Fatal(err)
	}

	s.ct = hyperpb.CompileMessageDescriptor(s.oracle.ProtoReflect().Descriptor())
	s.hm = hyperpb.NewMessage(s.ct)
	if err := s.hm.Unmarshal(s.wire); err != nil {
		b.Fatal(err)
	}

	s.goValue = newGo()
	if err := stdjson.Unmarshal(s.canonical, s.goValue); err != nil {
		b.Fatal(err)
	}
	return s
}

func BenchmarkMarshal(b *testing.B) {
	for _, tc := range benchCases {
		s := setup(b, tc.msg, tc.json, tc.newGo)

		run := func(name string, fn func() error) {
			b.Run(tc.name+"/"+name, func(b *testing.B) {
				b.ReportAllocs()
				for range b.N {
					if err := fn(); err != nil {
						b.Fatal(err)
					}
				}
			})
		}

		// JSON producers.
		run("json/hyperjson_hyperpb", func() error {
			_, err := hyperjson.Marshal(s.hm)
			return err
		})
		run("json/protojson_hyperpb", func() error {
			_, err := protojson.Marshal(s.hm)
			return err
		})
		run("json/protojson_gencode", func() error {
			_, err := protojson.Marshal(s.oracle)
			return err
		})
		run("json/encjson_gostruct", func() error {
			_, err := stdjson.Marshal(s.goValue)
			return err
		})
		run("json/jsonv2_gostruct", func() error {
			_, err := jsonv2.Marshal(s.goValue)
			return err
		})

		// Binary wire producers, for scale.
		run("wire/proto_gencode", func() error {
			_, err := proto.Marshal(s.oracle)
			return err
		})
		run("wire/proto_hyperpb_reflect", func() error {
			// hyperpb has no native serializer; this is generic
			// reflection-based marshaling over the hyperpb message.
			_, err := proto.Marshal(s.hm)
			return err
		})
	}
}

func BenchmarkUnmarshal(b *testing.B) {
	for _, tc := range benchCases {
		s := setup(b, tc.msg, tc.json, tc.newGo)
		md := tc.msg.ProtoReflect().Descriptor()

		// JSON consumers, all parsing the identical canonical document.
		b.Run(tc.name+"/json/hyperjson_hyperpb", func(b *testing.B) {
			b.ReportAllocs()
			shared := new(hyperpb.Shared)
			for range b.N {
				hm := shared.NewMessage(s.ct)
				if err := hyperjson.Unmarshal(s.canonical, hm); err != nil {
					b.Fatal(err)
				}
				shared.Free()
			}
		})
		b.Run(tc.name+"/json/protojson_dynamicpb", func(b *testing.B) {
			b.ReportAllocs()
			for range b.N {
				dm := dynamicpb.NewMessage(md)
				if err := protojson.Unmarshal(s.canonical, dm); err != nil {
					b.Fatal(err)
				}
			}
		})
		b.Run(tc.name+"/json/protojson_gencode", func(b *testing.B) {
			b.ReportAllocs()
			for range b.N {
				m := proto.Clone(tc.msg)
				if err := protojson.Unmarshal(s.canonical, m); err != nil {
					b.Fatal(err)
				}
			}
		})
		b.Run(tc.name+"/json/encjson_gostruct", func(b *testing.B) {
			b.ReportAllocs()
			for range b.N {
				if err := stdjson.Unmarshal(s.canonical, tc.newGo()); err != nil {
					b.Fatal(err)
				}
			}
		})
		b.Run(tc.name+"/json/jsonv2_gostruct", func(b *testing.B) {
			b.ReportAllocs()
			for range b.N {
				if err := jsonv2.Unmarshal(s.canonical, tc.newGo()); err != nil {
					b.Fatal(err)
				}
			}
		})

		// Binary wire consumers, for scale.
		b.Run(tc.name+"/wire/hyperpb", func(b *testing.B) {
			b.ReportAllocs()
			shared := new(hyperpb.Shared)
			for range b.N {
				hm := shared.NewMessage(s.ct)
				if err := hm.Unmarshal(s.wire); err != nil {
					b.Fatal(err)
				}
				shared.Free()
			}
		})
		b.Run(tc.name+"/wire/proto_gencode", func(b *testing.B) {
			b.ReportAllocs()
			for range b.N {
				m := proto.Clone(tc.msg)
				if err := proto.Unmarshal(s.wire, m); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
