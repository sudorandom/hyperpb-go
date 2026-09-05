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

package structenc

import (
	"cmp"
	"errors"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"unsafe"

	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

var wireTypes = [...]protowire.Type{
	0:                         0,
	protoreflect.BoolKind:     protowire.VarintType,
	protoreflect.EnumKind:     protowire.VarintType,
	protoreflect.Int32Kind:    protowire.VarintType,
	protoreflect.Sint32Kind:   protowire.VarintType,
	protoreflect.Uint32Kind:   protowire.VarintType,
	protoreflect.Int64Kind:    protowire.VarintType,
	protoreflect.Sint64Kind:   protowire.VarintType,
	protoreflect.Uint64Kind:   protowire.VarintType,
	protoreflect.Sfixed32Kind: protowire.Fixed32Type,
	protoreflect.Fixed32Kind:  protowire.Fixed32Type,
	protoreflect.FloatKind:    protowire.Fixed32Type,
	protoreflect.Sfixed64Kind: protowire.Fixed64Type,
	protoreflect.Fixed64Kind:  protowire.Fixed64Type,
	protoreflect.DoubleKind:   protowire.Fixed64Type,
	protoreflect.StringKind:   protowire.BytesType,
	protoreflect.BytesKind:    protowire.BytesType,
	protoreflect.MessageKind:  protowire.BytesType,
	protoreflect.GroupKind:    protowire.StartGroupType,
}

var errUnsupportedType = errors.New("structenc: unsupported type for encoding")

func compileStruct(t reflect.Type, visited map[reflect.Type]*Encoder) (*Encoder, error) {
	if t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return nil, errUnsupportedType
	}
	if enc, ok := visited[t]; ok {
		return enc, nil
	}

	dummy := reflect.New(t).Interface()
	protoMsg, ok := dummy.(proto.Message)
	if !ok {
		return nil, errUnsupportedType
	}

	pm := protoMsg.ProtoReflect()
	md := pm.Descriptor()

	enc := &Encoder{
		structType: t,
	}
	visited[t] = enc

	seenOneofs := make(map[string]bool)

	for i := range t.NumField() {
		sf := t.Field(i)
		if sf.Name == "unknownFields" && sf.Type == reflect.TypeFor[[]byte]() {
			enc.unknownOffset = sf.Offset
			enc.hasUnknown = true
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
				variants := make(map[reflect.Type]*oneofVariant)
				var minFieldNum protowire.Number
				for j := range od.Fields().Len() {
					fd := od.Fields().Get(j)
					if minFieldNum == 0 || fd.Number() < minFieldNum {
						minFieldNum = fd.Number()
					}
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

					var innerEncode encodeFunc
					var subEnc *Encoder
					if fd.Kind() == protoreflect.MessageKind {
						elemType := innerField.Type.Elem()
						var err error
						subEnc, err = compileStruct(elemType, visited)
						if err != nil {
							return nil, err
						}
						innerEncode = encodeSubMessage
					} else {
						innerEncode = selectOneofThunk(fd.Kind())
					}

					variant := &oneofVariant{
						num:    fd.Number(),
						offset: innerField.Offset,
						encode: innerEncode,
						subEnc: subEnc,
					}
					tag := protowire.EncodeTag(fd.Number(), wireTypes[fd.Kind()])
					tagSlice := protowire.AppendVarint(variant.tagBytes[:0], tag)
					variant.tagLen = uint8(len(tagSlice))
					variants[wrapperPtrType] = variant
				}

				oneofEnc := &oneofFieldEncoder{
					fieldIndex: i,
					parentType: t,
					variants:   variants,
				}
				enc.fields = append(enc.fields, fieldEntry{
					num:      minFieldNum,
					offset:   sf.Offset,
					encode:   oneofEnc.encode,
					oneofEnc: oneofEnc,
				})
			}
			continue
		}

		// Regular protobuf tag
		tagStr := sf.Tag.Get("protobuf")
		if tagStr == "" {
			continue
		}

		fieldNum, isReq, err := parseFieldTag(tagStr)
		if err != nil {
			continue
		}

		fd := md.Fields().ByNumber(fieldNum)
		if fd == nil {
			continue
		}

		if isReq || fd.Cardinality() == protoreflect.Required {
			enc.reqFields = append(enc.reqFields, reqFieldInfo{
				offset: sf.Offset,
				name:   string(fd.Name()),
			})
		}

		// Case A: Map
		if fd.IsMap() {
			keyfd := fd.MapKey()
			valfd := fd.MapValue()
			var valSubEnc *Encoder
			if valfd.Kind() == protoreflect.MessageKind {
				var err error
				valSubEnc, err = compileStruct(sf.Type.Elem().Elem(), visited)
				if err != nil {
					return nil, err
				}
			}
			mapEnc := &mapFieldEncoder{
				mapType:   sf.Type,
				keyKind:   keyfd.Kind(),
				valKind:   valfd.Kind(),
				valSubEnc: valSubEnc,
			}
			tag := protowire.EncodeTag(fieldNum, protowire.BytesType)
			entry := fieldEntry{
				num:    fieldNum,
				offset: sf.Offset,
				encode: mapEnc.encode,
				mapEnc: mapEnc,
			}
			tagSlice := protowire.AppendVarint(entry.tagBytes[:0], tag)
			entry.tagLen = uint8(len(tagSlice))
			enc.fields = append(enc.fields, entry)
			continue
		}

		// Case B: Repeated
		if fd.IsList() {
			elemType := sf.Type.Elem()
			var thunk encodeFunc
			var subEnc *Encoder
			wireType := protowire.BytesType

			switch {
			case elemType.Kind() == reflect.Pointer:
				var err error
				subEnc, err = compileStruct(elemType.Elem(), visited)
				if err != nil {
					return nil, err
				}
				thunk = encodeRepeatedSubMessage
			case elemType.Kind() == reflect.String:
				thunk = encodeRepeatedString
			case elemType == reflect.TypeFor[[]byte]():
				thunk = encodeRepeatedBytes
			default:
				if fd.IsPacked() {
					thunk = selectPackedThunk(fd.Kind())
				} else {
					wireType = wireTypes[fd.Kind()]
					thunk = selectUnpackedRepeatedThunk(fd.Kind())
				}
			}

			if thunk != nil {
				tag := protowire.EncodeTag(fieldNum, wireType)
				entry := fieldEntry{
					num:    fieldNum,
					offset: sf.Offset,
					encode: thunk,
					subEnc: subEnc,
				}
				tagSlice := protowire.AppendVarint(entry.tagBytes[:0], tag)
				entry.tagLen = uint8(len(tagSlice))
				enc.fields = append(enc.fields, entry)
			}
			continue
		}

		// Case C: Submessage
		if sf.Type.Kind() == reflect.Pointer && (fd.Kind() == protoreflect.MessageKind || fd.Kind() == protoreflect.GroupKind) {
			sub, err := compileStruct(sf.Type.Elem(), visited)
			if err != nil {
				return nil, err
			}
			var thunk encodeFunc
			var wireType protowire.Type
			if fd.Kind() == protoreflect.GroupKind {
				thunk = encodeGroup
				wireType = protowire.StartGroupType
			} else {
				thunk = encodeSubMessage
				wireType = protowire.BytesType
			}
			tag := protowire.EncodeTag(fieldNum, wireType)
			entry := fieldEntry{
				num:     fieldNum,
				offset:  sf.Offset,
				encode:  thunk,
				subEnc:  sub,
				isGroup: fd.Kind() == protoreflect.GroupKind,
			}
			tagSlice := protowire.AppendVarint(entry.tagBytes[:0], tag)
			entry.tagLen = uint8(len(tagSlice))
			enc.fields = append(enc.fields, entry)
			continue
		}

		// Case D: Optional Scalar Pointer (proto2 or proto3 optional)
		if sf.Type.Kind() == reflect.Pointer {
			thunk := selectOptionalThunk(fd.Kind())
			if thunk != nil {
				tag := protowire.EncodeTag(fieldNum, wireTypes[fd.Kind()])
				entry := fieldEntry{
					num:    fieldNum,
					offset: sf.Offset,
					encode: thunk,
				}
				tagSlice := protowire.AppendVarint(entry.tagBytes[:0], tag)
				entry.tagLen = uint8(len(tagSlice))
				enc.fields = append(enc.fields, entry)
			}
			continue
		}

		// Case E: Singular Non-Optional Scalar (proto3)
		thunk := selectSingularThunk(fd.Kind())
		if thunk != nil {
			tag := protowire.EncodeTag(fieldNum, wireTypes[fd.Kind()])
			entry := fieldEntry{
				num:    fieldNum,
				offset: sf.Offset,
				encode: thunk,
			}
			tagSlice := protowire.AppendVarint(entry.tagBytes[:0], tag)
			entry.tagLen = uint8(len(tagSlice))
			enc.fields = append(enc.fields, entry)
		}
	}

	// Sort fields by field number ascending (canonical protobuf encoding order)
	slices.SortFunc(enc.fields, func(a, b fieldEntry) int {
		return cmp.Compare(a.num, b.num)
	})

	return enc, nil
}

func parseFieldTag(tag string) (protowire.Number, bool, error) {
	parts := strings.Split(tag, ",")
	if len(parts) < 2 {
		return 0, false, errors.New("invalid tag")
	}
	num, err := strconv.ParseUint(parts[1], 10, 32)
	if err != nil {
		return 0, false, err
	}
	isReq := slices.Contains(parts[2:], "req")
	return protowire.Number(num), isReq, nil
}

func selectSingularThunk(kind protoreflect.Kind) encodeFunc {
	switch kind {
	case protoreflect.Int32Kind, protoreflect.EnumKind:
		return encodeInt32
	case protoreflect.Sint32Kind:
		return encodeSint32
	case protoreflect.Uint32Kind:
		return encodeUint32
	case protoreflect.Int64Kind:
		return encodeInt64
	case protoreflect.Sint64Kind:
		return encodeSint64
	case protoreflect.Uint64Kind:
		return encodeUint64
	case protoreflect.BoolKind:
		return encodeBool
	case protoreflect.Fixed32Kind, protoreflect.Sfixed32Kind:
		return encodeFixed32
	case protoreflect.Fixed64Kind, protoreflect.Sfixed64Kind:
		return encodeFixed64
	case protoreflect.FloatKind:
		return encodeFloat32
	case protoreflect.DoubleKind:
		return encodeFloat64
	case protoreflect.StringKind:
		return encodeString
	case protoreflect.BytesKind:
		return encodeBytes
	default:
		return nil
	}
}

func selectOptionalThunk(kind protoreflect.Kind) encodeFunc {
	switch kind {
	case protoreflect.Int32Kind, protoreflect.EnumKind:
		return encodeOptInt32
	case protoreflect.Sint32Kind:
		return encodeOptSint32
	case protoreflect.Uint32Kind:
		return encodeOptUint32
	case protoreflect.Int64Kind:
		return encodeOptInt64
	case protoreflect.Sint64Kind:
		return encodeOptSint64
	case protoreflect.Uint64Kind:
		return encodeOptUint64
	case protoreflect.BoolKind:
		return encodeOptBool
	case protoreflect.Fixed32Kind, protoreflect.Sfixed32Kind:
		return encodeOptFixed32
	case protoreflect.Fixed64Kind, protoreflect.Sfixed64Kind:
		return encodeOptFixed64
	case protoreflect.FloatKind:
		return encodeOptFloat32
	case protoreflect.DoubleKind:
		return encodeOptFloat64
	case protoreflect.StringKind:
		return encodeOptString
	case protoreflect.BytesKind:
		return encodeOptBytes
	default:
		return nil
	}
}

func selectPackedThunk(kind protoreflect.Kind) encodeFunc {
	switch kind {
	case protoreflect.Int32Kind, protoreflect.EnumKind:
		return encodePackedInt32
	case protoreflect.Sint32Kind:
		return encodePackedSint32
	case protoreflect.Uint32Kind:
		return encodePackedUint32
	case protoreflect.Int64Kind:
		return encodePackedInt64
	case protoreflect.Sint64Kind:
		return encodePackedSint64
	case protoreflect.Uint64Kind:
		return encodePackedUint64
	case protoreflect.BoolKind:
		return encodePackedBool
	case protoreflect.Fixed32Kind, protoreflect.Sfixed32Kind, protoreflect.FloatKind:
		return encodePackedFixed32
	case protoreflect.Fixed64Kind, protoreflect.Sfixed64Kind, protoreflect.DoubleKind:
		return encodePackedFixed64
	default:
		return nil
	}
}

func selectUnpackedRepeatedThunk(kind protoreflect.Kind) encodeFunc {
	switch kind {
	case protoreflect.Int32Kind, protoreflect.EnumKind:
		return func(b []byte, base unsafe.Pointer, f *fieldEntry, _ Options) ([]byte, error) {
			s := *(*[]int32)(unsafe.Add(base, f.offset))
			for _, v := range s {
				b = append(b, f.tagBytes[:f.tagLen]...)
				b = protowire.AppendVarint(b, uint64(v))
			}
			return b, nil
		}
	case protoreflect.Sint32Kind:
		return func(b []byte, base unsafe.Pointer, f *fieldEntry, _ Options) ([]byte, error) {
			s := *(*[]int32)(unsafe.Add(base, f.offset))
			for _, v := range s {
				b = append(b, f.tagBytes[:f.tagLen]...)
				b = protowire.AppendVarint(b, protowire.EncodeZigZag(int64(v)))
			}
			return b, nil
		}
	case protoreflect.Uint32Kind:
		return func(b []byte, base unsafe.Pointer, f *fieldEntry, _ Options) ([]byte, error) {
			s := *(*[]uint32)(unsafe.Add(base, f.offset))
			for _, v := range s {
				b = append(b, f.tagBytes[:f.tagLen]...)
				b = protowire.AppendVarint(b, uint64(v))
			}
			return b, nil
		}
	case protoreflect.Int64Kind:
		return func(b []byte, base unsafe.Pointer, f *fieldEntry, _ Options) ([]byte, error) {
			s := *(*[]int64)(unsafe.Add(base, f.offset))
			for _, v := range s {
				b = append(b, f.tagBytes[:f.tagLen]...)
				b = protowire.AppendVarint(b, uint64(v))
			}
			return b, nil
		}
	case protoreflect.Sint64Kind:
		return func(b []byte, base unsafe.Pointer, f *fieldEntry, _ Options) ([]byte, error) {
			s := *(*[]int64)(unsafe.Add(base, f.offset))
			for _, v := range s {
				b = append(b, f.tagBytes[:f.tagLen]...)
				b = protowire.AppendVarint(b, protowire.EncodeZigZag(v))
			}
			return b, nil
		}
	case protoreflect.Uint64Kind:
		return func(b []byte, base unsafe.Pointer, f *fieldEntry, _ Options) ([]byte, error) {
			s := *(*[]uint64)(unsafe.Add(base, f.offset))
			for _, v := range s {
				b = append(b, f.tagBytes[:f.tagLen]...)
				b = protowire.AppendVarint(b, v)
			}
			return b, nil
		}
	case protoreflect.BoolKind:
		return func(b []byte, base unsafe.Pointer, f *fieldEntry, _ Options) ([]byte, error) {
			s := *(*[]bool)(unsafe.Add(base, f.offset))
			for _, v := range s {
				b = append(b, f.tagBytes[:f.tagLen]...)
				if v {
					b = append(b, 1)
				} else {
					b = append(b, 0)
				}
			}
			return b, nil
		}
	case protoreflect.Fixed32Kind, protoreflect.Sfixed32Kind, protoreflect.FloatKind:
		return func(b []byte, base unsafe.Pointer, f *fieldEntry, _ Options) ([]byte, error) {
			s := *(*[]uint32)(unsafe.Add(base, f.offset))
			for _, v := range s {
				b = append(b, f.tagBytes[:f.tagLen]...)
				b = protowire.AppendFixed32(b, v)
			}
			return b, nil
		}
	case protoreflect.Fixed64Kind, protoreflect.Sfixed64Kind, protoreflect.DoubleKind:
		return func(b []byte, base unsafe.Pointer, f *fieldEntry, _ Options) ([]byte, error) {
			s := *(*[]uint64)(unsafe.Add(base, f.offset))
			for _, v := range s {
				b = append(b, f.tagBytes[:f.tagLen]...)
				b = protowire.AppendFixed64(b, v)
			}
			return b, nil
		}
	default:
		return nil
	}
}

func selectOneofThunk(kind protoreflect.Kind) encodeFunc {
	switch kind {
	case protoreflect.Int32Kind, protoreflect.EnumKind:
		return encodeOneofInt32
	case protoreflect.Sint32Kind:
		return encodeOneofSint32
	case protoreflect.Uint32Kind:
		return encodeOneofUint32
	case protoreflect.Int64Kind:
		return encodeOneofInt64
	case protoreflect.Sint64Kind:
		return encodeOneofSint64
	case protoreflect.Uint64Kind:
		return encodeOneofUint64
	case protoreflect.BoolKind:
		return encodeOneofBool
	case protoreflect.Fixed32Kind, protoreflect.Sfixed32Kind:
		return encodeOneofFixed32
	case protoreflect.Fixed64Kind, protoreflect.Sfixed64Kind:
		return encodeOneofFixed64
	case protoreflect.FloatKind:
		return encodeOneofFloat32
	case protoreflect.DoubleKind:
		return encodeOneofFloat64
	case protoreflect.StringKind:
		return encodeOneofString
	case protoreflect.BytesKind:
		return encodeOneofBytes
	default:
		return nil
	}
}
