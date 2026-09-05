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

package structdec

import (
	"errors"
	"math"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	"buf.build/go/hyperpb/internal/xsync"
)

var globalCache xsync.Map[reflect.Type, *Decoder]

// Get returns the compiled Decoder for msg, compiling and caching it if necessary.
func Get(msg proto.Message) (*Decoder, error) {
	if msg == nil {
		return nil, errors.New("structdec: message is nil")
	}
	t := reflect.TypeOf(msg)
	if dec, ok := globalCache.Load(t); ok {
		return dec, nil
	}
	dec, err := Compile(t)
	if err != nil {
		return nil, err
	}
	globalCache.Store(t, dec)
	return dec, nil
}

// Compile compiles a Decoder for the given Go type t (must be a struct or pointer to struct).
func Compile(t reflect.Type) (*Decoder, error) {
	visited := make(map[reflect.Type]*Decoder)
	return compileStruct(t, visited)
}

func compileStruct(t reflect.Type, visited map[reflect.Type]*Decoder) (*Decoder, error) {
	if t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return nil, errUnsupportedType
	}
	if dec, ok := visited[t]; ok {
		return dec, nil
	}

	dummy := reflect.New(t).Interface()
	protoMsg, ok := dummy.(proto.Message)
	if !ok {
		return nil, errUnsupportedType
	}

	pm := protoMsg.ProtoReflect()
	md := pm.Descriptor()

	d := &Decoder{
		structType: t,
		tagMap:     make(map[uint64]uint16),
	}
	for i := range d.lut {
		d.lut[i] = 0xff
	}
	visited[t] = d

	// Track oneofs processed to avoid duplicating oneof interfaces
	seenOneofs := make(map[string]bool)

	for i := range t.NumField() {
		sf := t.Field(i)
		if sf.Name == "unknownFields" && sf.Type == reflect.TypeFor[[]byte]() {
			d.unknownOffset = sf.Offset
			d.hasUnknown = true
			continue
		}

		// Check oneof interface
		oneofTag := sf.Tag.Get("protobuf_oneof")
		if oneofTag != "" {
			if seenOneofs[oneofTag] {
				continue
			}
			seenOneofs[oneofTag] = true
			od := md.Oneofs().ByName(protoreflect.Name(oneofTag))
			if od != nil {
				for j := range od.Fields().Len() {
					fd := od.Fields().Get(j)
					// Sample message to discover concrete wrapper struct
					sample, ok := reflect.New(t).Interface().(proto.Message)
					if !ok {
						continue
					}
					spm := sample.ProtoReflect()
					spm.Set(fd, spm.NewField(fd))
					wrapperVal := reflect.ValueOf(sample).Elem().Field(i)
					if wrapperVal.IsNil() {
						continue
					}
					wrapperPtrType := wrapperVal.Elem().Type()
					wrapperStructType := wrapperPtrType.Elem()
					innerField := wrapperStructType.Field(0)

					var innerDecode decodeFunc
					var isMessage bool
					var subDec *subMessageDecoder

					if fd.Kind() == protoreflect.MessageKind {
						elemType := innerField.Type.Elem()
						sub, err := compileStruct(elemType, visited)
						if err != nil {
							return nil, err
						}
						subDec = &subMessageDecoder{subDec: sub, elemType: elemType}
						innerDecode = subDec.decodeSingular
						isMessage = true
					} else {
						innerDecode = selectSingularThunk(fd.Kind())
					}

					oneofDec := &oneofFieldDecoder{
						fieldIndex:  sf.Index,
						parentType:  t,
						wrapperType: wrapperPtrType,
						structType:  wrapperStructType,
						innerOffset: innerField.Offset,
						innerDecode: innerDecode,
						isMessage:   isMessage,
						subDec:      subDec,
					}

					wireType := wireTypeFromKind(fd.Kind())
					tag := protowire.EncodeTag(fd.Number(), wireType)
					d.registerTag(tag, fieldEntry{
						offset:  sf.Offset,
						isOneof: true,
						decode:  oneofDec.decode,
					})
				}
			}
			continue
		}

		// Check regular protobuf tag
		protoTag := sf.Tag.Get("protobuf")
		if protoTag == "" {
			continue
		}

		parts := strings.Split(protoTag, ",")
		if len(parts) < 2 {
			continue
		}

		fieldNumInt, err := strconv.Atoi(parts[1])
		if err != nil {
			continue
		}
		fieldNum := protowire.Number(fieldNumInt)
		isRep := slices.Contains(parts, "rep")
		isReq := slices.Contains(parts, "req")

		if isReq {
			d.reqFields = append(d.reqFields, reqFieldInfo{offset: sf.Offset, name: sf.Name})
		}

		fd := md.Fields().ByNumber(fieldNum)
		if fd == nil {
			continue
		}

		// Case A: Map
		if fd.IsMap() {
			keyFd := fd.MapKey()
			valFd := fd.MapValue()
			mapDec, err := compileMapDecoder(sf.Type, keyFd, valFd, visited)
			if err != nil {
				return nil, err
			}
			tag := protowire.EncodeTag(fieldNum, protowire.BytesType)
			d.registerTag(tag, fieldEntry{
				offset: sf.Offset,
				decode: mapDec.decode,
			})
			continue
		}

		// Case B: Repeated
		if isRep || fd.IsList() {
			elemType := sf.Type.Elem()
			switch {
			case elemType.Kind() == reflect.Pointer:
				// Repeated message
				sub, err := compileStruct(elemType.Elem(), visited)
				if err != nil {
					return nil, err
				}
				subDec := &subMessageDecoder{subDec: sub, elemType: elemType.Elem()}
				tag := protowire.EncodeTag(fieldNum, protowire.BytesType)
				d.registerTag(tag, fieldEntry{
					offset: sf.Offset,
					decode: subDec.decodeRepeated,
				})
			case elemType.Kind() == reflect.String:
				tag := protowire.EncodeTag(fieldNum, protowire.BytesType)
				d.registerTag(tag, fieldEntry{
					offset: sf.Offset,
					decode: decodeRepeatedString,
				})
			case elemType == reflect.TypeFor[[]byte]():
				tag := protowire.EncodeTag(fieldNum, protowire.BytesType)
				d.registerTag(tag, fieldEntry{
					offset: sf.Offset,
					decode: decodeRepeatedBytes,
				})
			default:
				// Packed & unpacked numeric/bool/enum
				packedThunk, unpackedThunk, unpackedWireType := selectRepeatedThunks(fd.Kind())
				if packedThunk != nil && unpackedThunk != nil {
					packedTag := protowire.EncodeTag(fieldNum, protowire.BytesType)
					unpackedTag := protowire.EncodeTag(fieldNum, unpackedWireType)
					d.registerTag(packedTag, fieldEntry{
						offset: sf.Offset,
						decode: packedThunk,
					})
					d.registerTag(unpackedTag, fieldEntry{
						offset: sf.Offset,
						decode: unpackedThunk,
					})
				}
			}
			continue
		}

		// Case C: Submessage
		if sf.Type.Kind() == reflect.Pointer && fd.Kind() == protoreflect.MessageKind {
			sub, err := compileStruct(sf.Type.Elem(), visited)
			if err != nil {
				return nil, err
			}
			subDec := &subMessageDecoder{subDec: sub, elemType: sf.Type.Elem()}
			tag := protowire.EncodeTag(fieldNum, protowire.BytesType)
			d.registerTag(tag, fieldEntry{
				offset: sf.Offset,
				decode: subDec.decodeSingular,
			})
			continue
		}

		// Case D: Optional scalar (pointer)
		if sf.Type.Kind() == reflect.Pointer {
			wireType := wireTypeFromKind(fd.Kind())
			thunk := selectOptThunk(fd.Kind())
			if thunk != nil {
				tag := protowire.EncodeTag(fieldNum, wireType)
				d.registerTag(tag, fieldEntry{
					offset: sf.Offset,
					decode: thunk,
				})
			}
			continue
		}

		// Case E: Singular direct primitive / string / bytes
		wireType := wireTypeFromKind(fd.Kind())
		thunk := selectSingularThunk(fd.Kind())
		if thunk != nil {
			tag := protowire.EncodeTag(fieldNum, wireType)
			d.registerTag(tag, fieldEntry{
				offset: sf.Offset,
				decode: thunk,
			})
		}
	}

	return d, nil
}

func (d *Decoder) registerTag(tag uint64, entry fieldEntry) {
	idx := uint16(len(d.fields))
	d.fields = append(d.fields, entry)
	if tag < 128 {
		d.lut[tag] = uint8(idx)
	} else {
		d.tagMap[tag] = idx
	}
}

func wireTypeFromKind(k protoreflect.Kind) protowire.Type {
	switch k {
	case protoreflect.BoolKind, protoreflect.EnumKind,
		protoreflect.Int32Kind, protoreflect.Int64Kind,
		protoreflect.Uint32Kind, protoreflect.Uint64Kind,
		protoreflect.Sint32Kind, protoreflect.Sint64Kind:
		return protowire.VarintType
	case protoreflect.Fixed32Kind, protoreflect.Sfixed32Kind, protoreflect.FloatKind:
		return protowire.Fixed32Type
	case protoreflect.Fixed64Kind, protoreflect.Sfixed64Kind, protoreflect.DoubleKind:
		return protowire.Fixed64Type
	case protoreflect.StringKind, protoreflect.BytesKind, protoreflect.MessageKind:
		return protowire.BytesType
	case protoreflect.GroupKind:
		return protowire.StartGroupType
	default:
		return protowire.BytesType
	}
}

func selectSingularThunk(k protoreflect.Kind) decodeFunc {
	switch k {
	case protoreflect.Int32Kind, protoreflect.EnumKind:
		return decodeInt32
	case protoreflect.Int64Kind:
		return decodeInt64
	case protoreflect.Uint32Kind:
		return decodeUint32
	case protoreflect.Uint64Kind:
		return decodeUint64
	case protoreflect.Sint32Kind:
		return decodeSint32
	case protoreflect.Sint64Kind:
		return decodeSint64
	case protoreflect.BoolKind:
		return decodeBool
	case protoreflect.Fixed32Kind:
		return decodeFixed32
	case protoreflect.Sfixed32Kind:
		return decodeSfixed32
	case protoreflect.FloatKind:
		return decodeFloat32
	case protoreflect.Fixed64Kind:
		return decodeFixed64
	case protoreflect.Sfixed64Kind:
		return decodeSfixed64
	case protoreflect.DoubleKind:
		return decodeFloat64
	case protoreflect.StringKind:
		return decodeString
	case protoreflect.BytesKind:
		return decodeBytes
	default:
		return nil
	}
}

func selectOptThunk(k protoreflect.Kind) decodeFunc {
	switch k {
	case protoreflect.Int32Kind, protoreflect.EnumKind:
		return decodeOptInt32
	case protoreflect.Int64Kind:
		return decodeOptInt64
	case protoreflect.Uint32Kind:
		return decodeOptUint32
	case protoreflect.Uint64Kind:
		return decodeOptUint64
	case protoreflect.Sint32Kind:
		return decodeOptSint32
	case protoreflect.Sint64Kind:
		return decodeOptSint64
	case protoreflect.BoolKind:
		return decodeOptBool
	case protoreflect.Fixed32Kind:
		return decodeOptFixed32
	case protoreflect.Sfixed32Kind:
		return decodeOptSfixed32
	case protoreflect.FloatKind:
		return decodeOptFloat32
	case protoreflect.Fixed64Kind:
		return decodeOptFixed64
	case protoreflect.Sfixed64Kind:
		return decodeOptSfixed64
	case protoreflect.DoubleKind:
		return decodeOptFloat64
	case protoreflect.StringKind:
		return decodeOptString
	default:
		return nil
	}
}

func selectRepeatedThunks(k protoreflect.Kind) (packed decodeFunc, unpacked decodeFunc, wireType protowire.Type) {
	switch k {
	case protoreflect.Int32Kind, protoreflect.EnumKind:
		return decodePackedInt32, decodeUnpackedInt32, protowire.VarintType
	case protoreflect.Int64Kind:
		return decodePackedInt64, decodeUnpackedInt64, protowire.VarintType
	case protoreflect.Uint32Kind:
		return decodePackedUint32, decodeUnpackedUint32, protowire.VarintType
	case protoreflect.Uint64Kind:
		return decodePackedUint64, decodeUnpackedUint64, protowire.VarintType
	case protoreflect.Sint32Kind:
		return decodePackedSint32, decodeUnpackedSint32, protowire.VarintType
	case protoreflect.Sint64Kind:
		return decodePackedSint64, decodeUnpackedSint64, protowire.VarintType
	case protoreflect.BoolKind:
		return decodePackedBool, decodeUnpackedBool, protowire.VarintType
	case protoreflect.Fixed32Kind:
		return decodePackedFixed32, decodeUnpackedFixed32, protowire.Fixed32Type
	case protoreflect.Sfixed32Kind:
		return decodePackedSfixed32, decodeUnpackedSfixed32, protowire.Fixed32Type
	case protoreflect.FloatKind:
		return decodePackedFloat32, decodeUnpackedFloat32, protowire.Fixed32Type
	case protoreflect.Fixed64Kind:
		return decodePackedFixed64, decodeUnpackedFixed64, protowire.Fixed64Type
	case protoreflect.Sfixed64Kind:
		return decodePackedSfixed64, decodeUnpackedSfixed64, protowire.Fixed64Type
	case protoreflect.DoubleKind:
		return decodePackedFloat64, decodeUnpackedFloat64, protowire.Fixed64Type
	default:
		return nil, nil, protowire.VarintType
	}
}

func compileMapDecoder(mapType reflect.Type, keyFd, valFd protoreflect.FieldDescriptor, visited map[reflect.Type]*Decoder) (*mapEntryDecoder, error) {
	keyType := mapType.Key()
	valType := mapType.Elem()

	keyDecoder := makeMapKeyDecoder(keyFd, keyType)
	valDecoder, err := makeMapValDecoder(valFd, valType, visited)
	if err != nil {
		return nil, err
	}

	return &mapEntryDecoder{
		mapType:    mapType,
		keyDecoder: keyDecoder,
		valDecoder: valDecoder,
		defKey:     reflect.Zero(keyType),
		defVal:     reflect.Zero(valType),
	}, nil
}

func makeMapKeyDecoder(fd protoreflect.FieldDescriptor, keyType reflect.Type) func(b []byte, opts Options) (reflect.Value, int, error) {
	switch fd.Kind() {
	case protoreflect.StringKind:
		return func(b []byte, opts Options) (reflect.Value, int, error) {
			v, n := protowire.ConsumeBytes(b)
			if n < 0 {
				return reflect.Value{}, 0, protowire.ParseError(n)
			}
			if !opts.AllowInvalidUTF8 && !utf8.Valid(v) {
				return reflect.Value{}, 0, errInvalidUTF8
			}
			return reflect.ValueOf(string(v)), n, nil
		}
	case protoreflect.Int32Kind:
		return func(b []byte, _ Options) (reflect.Value, int, error) {
			v, n := protowire.ConsumeVarint(b)
			if n < 0 {
				return reflect.Value{}, 0, protowire.ParseError(n)
			}
			return reflect.ValueOf(int32(v)), n, nil
		}
	case protoreflect.Int64Kind:
		return func(b []byte, _ Options) (reflect.Value, int, error) {
			v, n := protowire.ConsumeVarint(b)
			if n < 0 {
				return reflect.Value{}, 0, protowire.ParseError(n)
			}
			return reflect.ValueOf(int64(v)), n, nil
		}
	case protoreflect.Uint32Kind:
		return func(b []byte, _ Options) (reflect.Value, int, error) {
			v, n := protowire.ConsumeVarint(b)
			if n < 0 {
				return reflect.Value{}, 0, protowire.ParseError(n)
			}
			return reflect.ValueOf(uint32(v)), n, nil
		}
	case protoreflect.Uint64Kind:
		return func(b []byte, _ Options) (reflect.Value, int, error) {
			v, n := protowire.ConsumeVarint(b)
			if n < 0 {
				return reflect.Value{}, 0, protowire.ParseError(n)
			}
			return reflect.ValueOf(v), n, nil
		}
	case protoreflect.Sint32Kind:
		return func(b []byte, _ Options) (reflect.Value, int, error) {
			v, n := protowire.ConsumeVarint(b)
			if n < 0 {
				return reflect.Value{}, 0, protowire.ParseError(n)
			}
			return reflect.ValueOf(int32(protowire.DecodeZigZag(v))), n, nil
		}
	case protoreflect.Sint64Kind:
		return func(b []byte, _ Options) (reflect.Value, int, error) {
			v, n := protowire.ConsumeVarint(b)
			if n < 0 {
				return reflect.Value{}, 0, protowire.ParseError(n)
			}
			return reflect.ValueOf(protowire.DecodeZigZag(v)), n, nil
		}
	case protoreflect.Fixed32Kind:
		return func(b []byte, _ Options) (reflect.Value, int, error) {
			v, n := protowire.ConsumeFixed32(b)
			if n < 0 {
				return reflect.Value{}, 0, protowire.ParseError(n)
			}
			return reflect.ValueOf(v), n, nil
		}
	case protoreflect.Fixed64Kind:
		return func(b []byte, _ Options) (reflect.Value, int, error) {
			v, n := protowire.ConsumeFixed64(b)
			if n < 0 {
				return reflect.Value{}, 0, protowire.ParseError(n)
			}
			return reflect.ValueOf(v), n, nil
		}
	case protoreflect.Sfixed32Kind:
		return func(b []byte, _ Options) (reflect.Value, int, error) {
			v, n := protowire.ConsumeFixed32(b)
			if n < 0 {
				return reflect.Value{}, 0, protowire.ParseError(n)
			}
			return reflect.ValueOf(int32(v)), n, nil
		}
	case protoreflect.Sfixed64Kind:
		return func(b []byte, _ Options) (reflect.Value, int, error) {
			v, n := protowire.ConsumeFixed64(b)
			if n < 0 {
				return reflect.Value{}, 0, protowire.ParseError(n)
			}
			return reflect.ValueOf(int64(v)), n, nil
		}
	case protoreflect.BoolKind:
		return func(b []byte, _ Options) (reflect.Value, int, error) {
			v, n := protowire.ConsumeVarint(b)
			if n < 0 {
				return reflect.Value{}, 0, protowire.ParseError(n)
			}
			return reflect.ValueOf(v != 0), n, nil
		}
	default:
		return func(_ []byte, _ Options) (reflect.Value, int, error) {
			return reflect.Zero(keyType), 0, nil
		}
	}
}

func makeMapValDecoder(fd protoreflect.FieldDescriptor, valType reflect.Type, visited map[reflect.Type]*Decoder) (func(b []byte, opts Options) (reflect.Value, int, error), error) {
	switch fd.Kind() {
	case protoreflect.MessageKind:
		sub, err := compileStruct(valType.Elem(), visited)
		if err != nil {
			return nil, err
		}
		elemType := valType.Elem()
		return func(b []byte, opts Options) (reflect.Value, int, error) {
			buf, n := protowire.ConsumeBytes(b)
			if n < 0 {
				return reflect.Value{}, 0, protowire.ParseError(n)
			}
			newSub := reflect.New(elemType)
			subOpts := opts
			subOpts.MaxDepth--
			if err := sub.Decode(newSub.UnsafePointer(), buf, subOpts); err != nil {
				return reflect.Value{}, 0, err
			}
			return newSub, n, nil
		}, nil
	case protoreflect.StringKind:
		return func(b []byte, opts Options) (reflect.Value, int, error) {
			v, n := protowire.ConsumeBytes(b)
			if n < 0 {
				return reflect.Value{}, 0, protowire.ParseError(n)
			}
			if !opts.AllowInvalidUTF8 && !utf8.Valid(v) {
				return reflect.Value{}, 0, errInvalidUTF8
			}
			return reflect.ValueOf(string(v)), n, nil
		}, nil
	case protoreflect.BytesKind:
		return func(b []byte, opts Options) (reflect.Value, int, error) {
			v, n := protowire.ConsumeBytes(b)
			if n < 0 {
				return reflect.Value{}, 0, protowire.ParseError(n)
			}
			if opts.AllowAlias {
				return reflect.ValueOf(v), n, nil
			}
			return reflect.ValueOf(bytesClone(v)), n, nil
		}, nil
	case protoreflect.Int32Kind, protoreflect.EnumKind:
		return func(b []byte, _ Options) (reflect.Value, int, error) {
			v, n := protowire.ConsumeVarint(b)
			if n < 0 {
				return reflect.Value{}, 0, protowire.ParseError(n)
			}
			return reflect.ValueOf(int32(v)).Convert(valType), n, nil
		}, nil
	case protoreflect.Int64Kind:
		return func(b []byte, _ Options) (reflect.Value, int, error) {
			v, n := protowire.ConsumeVarint(b)
			if n < 0 {
				return reflect.Value{}, 0, protowire.ParseError(n)
			}
			return reflect.ValueOf(int64(v)), n, nil
		}, nil
	case protoreflect.Uint32Kind:
		return func(b []byte, _ Options) (reflect.Value, int, error) {
			v, n := protowire.ConsumeVarint(b)
			if n < 0 {
				return reflect.Value{}, 0, protowire.ParseError(n)
			}
			return reflect.ValueOf(uint32(v)), n, nil
		}, nil
	case protoreflect.Uint64Kind:
		return func(b []byte, _ Options) (reflect.Value, int, error) {
			v, n := protowire.ConsumeVarint(b)
			if n < 0 {
				return reflect.Value{}, 0, protowire.ParseError(n)
			}
			return reflect.ValueOf(v), n, nil
		}, nil
	case protoreflect.Sint32Kind:
		return func(b []byte, _ Options) (reflect.Value, int, error) {
			v, n := protowire.ConsumeVarint(b)
			if n < 0 {
				return reflect.Value{}, 0, protowire.ParseError(n)
			}
			return reflect.ValueOf(int32(protowire.DecodeZigZag(v))), n, nil
		}, nil
	case protoreflect.Sint64Kind:
		return func(b []byte, _ Options) (reflect.Value, int, error) {
			v, n := protowire.ConsumeVarint(b)
			if n < 0 {
				return reflect.Value{}, 0, protowire.ParseError(n)
			}
			return reflect.ValueOf(protowire.DecodeZigZag(v)), n, nil
		}, nil
	case protoreflect.BoolKind:
		return func(b []byte, _ Options) (reflect.Value, int, error) {
			v, n := protowire.ConsumeVarint(b)
			if n < 0 {
				return reflect.Value{}, 0, protowire.ParseError(n)
			}
			return reflect.ValueOf(v != 0), n, nil
		}, nil
	case protoreflect.Fixed32Kind:
		return func(b []byte, _ Options) (reflect.Value, int, error) {
			v, n := protowire.ConsumeFixed32(b)
			if n < 0 {
				return reflect.Value{}, 0, protowire.ParseError(n)
			}
			return reflect.ValueOf(v), n, nil
		}, nil
	case protoreflect.Fixed64Kind:
		return func(b []byte, _ Options) (reflect.Value, int, error) {
			v, n := protowire.ConsumeFixed64(b)
			if n < 0 {
				return reflect.Value{}, 0, protowire.ParseError(n)
			}
			return reflect.ValueOf(v), n, nil
		}, nil
	case protoreflect.Sfixed32Kind:
		return func(b []byte, _ Options) (reflect.Value, int, error) {
			v, n := protowire.ConsumeFixed32(b)
			if n < 0 {
				return reflect.Value{}, 0, protowire.ParseError(n)
			}
			return reflect.ValueOf(int32(v)), n, nil
		}, nil
	case protoreflect.Sfixed64Kind:
		return func(b []byte, _ Options) (reflect.Value, int, error) {
			v, n := protowire.ConsumeFixed64(b)
			if n < 0 {
				return reflect.Value{}, 0, protowire.ParseError(n)
			}
			return reflect.ValueOf(int64(v)), n, nil
		}, nil
	case protoreflect.FloatKind:
		return func(b []byte, _ Options) (reflect.Value, int, error) {
			v, n := protowire.ConsumeFixed32(b)
			if n < 0 {
				return reflect.Value{}, 0, protowire.ParseError(n)
			}
			return reflect.ValueOf(math.Float32frombits(v)), n, nil
		}, nil
	case protoreflect.DoubleKind:
		return func(b []byte, _ Options) (reflect.Value, int, error) {
			v, n := protowire.ConsumeFixed64(b)
			if n < 0 {
				return reflect.Value{}, 0, protowire.ParseError(n)
			}
			return reflect.ValueOf(math.Float64frombits(v)), n, nil
		}, nil
	default:
		return func(_ []byte, _ Options) (reflect.Value, int, error) {
			return reflect.Zero(valType), 0, nil
		}, nil
	}
}

func bytesClone(b []byte) []byte {
	c := make([]byte, len(b))
	copy(c, b)
	return c
}
