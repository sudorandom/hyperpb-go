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
	"bytes"
	"math"
	"reflect"
	"slices"
	"unicode/utf8"
	"unsafe"

	"google.golang.org/protobuf/encoding/protowire"
)

type decodeFunc func(target unsafe.Pointer, b []byte, opts Options) (int, error)

// Singular primitive scalars.

func decodeInt32(target unsafe.Pointer, b []byte, _ Options) (int, error) {
	v, n := protowire.ConsumeVarint(b)
	if n < 0 {
		return 0, protowire.ParseError(n)
	}
	*(*int32)(target) = int32(v)
	return n, nil
}

func decodeInt64(target unsafe.Pointer, b []byte, _ Options) (int, error) {
	v, n := protowire.ConsumeVarint(b)
	if n < 0 {
		return 0, protowire.ParseError(n)
	}
	*(*int64)(target) = int64(v)
	return n, nil
}

func decodeUint32(target unsafe.Pointer, b []byte, _ Options) (int, error) {
	v, n := protowire.ConsumeVarint(b)
	if n < 0 {
		return 0, protowire.ParseError(n)
	}
	*(*uint32)(target) = uint32(v)
	return n, nil
}

func decodeUint64(target unsafe.Pointer, b []byte, _ Options) (int, error) {
	v, n := protowire.ConsumeVarint(b)
	if n < 0 {
		return 0, protowire.ParseError(n)
	}
	*(*uint64)(target) = v
	return n, nil
}

func decodeSint32(target unsafe.Pointer, b []byte, _ Options) (int, error) {
	v, n := protowire.ConsumeVarint(b)
	if n < 0 {
		return 0, protowire.ParseError(n)
	}
	*(*int32)(target) = int32(protowire.DecodeZigZag(v))
	return n, nil
}

func decodeSint64(target unsafe.Pointer, b []byte, _ Options) (int, error) {
	v, n := protowire.ConsumeVarint(b)
	if n < 0 {
		return 0, protowire.ParseError(n)
	}
	*(*int64)(target) = protowire.DecodeZigZag(v)
	return n, nil
}

func decodeBool(target unsafe.Pointer, b []byte, _ Options) (int, error) {
	v, n := protowire.ConsumeVarint(b)
	if n < 0 {
		return 0, protowire.ParseError(n)
	}
	*(*bool)(target) = (v != 0)
	return n, nil
}

func decodeFixed32(target unsafe.Pointer, b []byte, _ Options) (int, error) {
	v, n := protowire.ConsumeFixed32(b)
	if n < 0 {
		return 0, protowire.ParseError(n)
	}
	*(*uint32)(target) = v
	return n, nil
}

func decodeSfixed32(target unsafe.Pointer, b []byte, _ Options) (int, error) {
	v, n := protowire.ConsumeFixed32(b)
	if n < 0 {
		return 0, protowire.ParseError(n)
	}
	*(*int32)(target) = int32(v)
	return n, nil
}

func decodeFloat32(target unsafe.Pointer, b []byte, _ Options) (int, error) {
	v, n := protowire.ConsumeFixed32(b)
	if n < 0 {
		return 0, protowire.ParseError(n)
	}
	*(*float32)(target) = math.Float32frombits(v)
	return n, nil
}

func decodeFixed64(target unsafe.Pointer, b []byte, _ Options) (int, error) {
	v, n := protowire.ConsumeFixed64(b)
	if n < 0 {
		return 0, protowire.ParseError(n)
	}
	*(*uint64)(target) = v
	return n, nil
}

func decodeSfixed64(target unsafe.Pointer, b []byte, _ Options) (int, error) {
	v, n := protowire.ConsumeFixed64(b)
	if n < 0 {
		return 0, protowire.ParseError(n)
	}
	*(*int64)(target) = int64(v)
	return n, nil
}

func decodeFloat64(target unsafe.Pointer, b []byte, _ Options) (int, error) {
	v, n := protowire.ConsumeFixed64(b)
	if n < 0 {
		return 0, protowire.ParseError(n)
	}
	*(*float64)(target) = math.Float64frombits(v)
	return n, nil
}

// Strings and bytes.

func decodeString(target unsafe.Pointer, b []byte, opts Options) (int, error) {
	v, n := protowire.ConsumeBytes(b)
	if n < 0 {
		return 0, protowire.ParseError(n)
	}
	if !opts.AllowInvalidUTF8 && !utf8.Valid(v) {
		return 0, errInvalidUTF8
	}
	if opts.AllowAlias {
		*(*string)(target) = unsafe.String(unsafe.SliceData(v), len(v))
	} else {
		*(*string)(target) = string(v)
	}
	return n, nil
}

func decodeBytes(target unsafe.Pointer, b []byte, opts Options) (int, error) {
	v, n := protowire.ConsumeBytes(b)
	if n < 0 {
		return 0, protowire.ParseError(n)
	}
	if opts.AllowAlias {
		*(*[]byte)(target) = v
	} else {
		*(*[]byte)(target) = bytes.Clone(v)
	}
	return n, nil
}

// Optional primitive scalars (pointers).

func decodeOptInt32(target unsafe.Pointer, b []byte, _ Options) (int, error) {
	v, n := protowire.ConsumeVarint(b)
	if n < 0 {
		return 0, protowire.ParseError(n)
	}
	p := *(**int32)(target)
	if p == nil {
		p = new(int32)
		*(**int32)(target) = p
	}
	*p = int32(v)
	return n, nil
}

func decodeOptInt64(target unsafe.Pointer, b []byte, _ Options) (int, error) {
	v, n := protowire.ConsumeVarint(b)
	if n < 0 {
		return 0, protowire.ParseError(n)
	}
	p := *(**int64)(target)
	if p == nil {
		p = new(int64)
		*(**int64)(target) = p
	}
	*p = int64(v)
	return n, nil
}

func decodeOptUint32(target unsafe.Pointer, b []byte, _ Options) (int, error) {
	v, n := protowire.ConsumeVarint(b)
	if n < 0 {
		return 0, protowire.ParseError(n)
	}
	p := *(**uint32)(target)
	if p == nil {
		p = new(uint32)
		*(**uint32)(target) = p
	}
	*p = uint32(v)
	return n, nil
}

func decodeOptUint64(target unsafe.Pointer, b []byte, _ Options) (int, error) {
	v, n := protowire.ConsumeVarint(b)
	if n < 0 {
		return 0, protowire.ParseError(n)
	}
	p := *(**uint64)(target)
	if p == nil {
		p = new(uint64)
		*(**uint64)(target) = p
	}
	*p = v
	return n, nil
}

func decodeOptSint32(target unsafe.Pointer, b []byte, _ Options) (int, error) {
	v, n := protowire.ConsumeVarint(b)
	if n < 0 {
		return 0, protowire.ParseError(n)
	}
	p := *(**int32)(target)
	if p == nil {
		p = new(int32)
		*(**int32)(target) = p
	}
	*p = int32(protowire.DecodeZigZag(v))
	return n, nil
}

func decodeOptSint64(target unsafe.Pointer, b []byte, _ Options) (int, error) {
	v, n := protowire.ConsumeVarint(b)
	if n < 0 {
		return 0, protowire.ParseError(n)
	}
	p := *(**int64)(target)
	if p == nil {
		p = new(int64)
		*(**int64)(target) = p
	}
	*p = protowire.DecodeZigZag(v)
	return n, nil
}

func decodeOptBool(target unsafe.Pointer, b []byte, _ Options) (int, error) {
	v, n := protowire.ConsumeVarint(b)
	if n < 0 {
		return 0, protowire.ParseError(n)
	}
	p := *(**bool)(target)
	if p == nil {
		p = new(bool)
		*(**bool)(target) = p
	}
	*p = (v != 0)
	return n, nil
}

func decodeOptFixed32(target unsafe.Pointer, b []byte, _ Options) (int, error) {
	v, n := protowire.ConsumeFixed32(b)
	if n < 0 {
		return 0, protowire.ParseError(n)
	}
	p := *(**uint32)(target)
	if p == nil {
		p = new(uint32)
		*(**uint32)(target) = p
	}
	*p = v
	return n, nil
}

func decodeOptSfixed32(target unsafe.Pointer, b []byte, _ Options) (int, error) {
	v, n := protowire.ConsumeFixed32(b)
	if n < 0 {
		return 0, protowire.ParseError(n)
	}
	p := *(**int32)(target)
	if p == nil {
		p = new(int32)
		*(**int32)(target) = p
	}
	*p = int32(v)
	return n, nil
}

func decodeOptFloat32(target unsafe.Pointer, b []byte, _ Options) (int, error) {
	v, n := protowire.ConsumeFixed32(b)
	if n < 0 {
		return 0, protowire.ParseError(n)
	}
	p := *(**float32)(target)
	if p == nil {
		p = new(float32)
		*(**float32)(target) = p
	}
	*p = math.Float32frombits(v)
	return n, nil
}

func decodeOptFixed64(target unsafe.Pointer, b []byte, _ Options) (int, error) {
	v, n := protowire.ConsumeFixed64(b)
	if n < 0 {
		return 0, protowire.ParseError(n)
	}
	p := *(**uint64)(target)
	if p == nil {
		p = new(uint64)
		*(**uint64)(target) = p
	}
	*p = v
	return n, nil
}

func decodeOptSfixed64(target unsafe.Pointer, b []byte, _ Options) (int, error) {
	v, n := protowire.ConsumeFixed64(b)
	if n < 0 {
		return 0, protowire.ParseError(n)
	}
	p := *(**int64)(target)
	if p == nil {
		p = new(int64)
		*(**int64)(target) = p
	}
	*p = int64(v)
	return n, nil
}

func decodeOptFloat64(target unsafe.Pointer, b []byte, _ Options) (int, error) {
	v, n := protowire.ConsumeFixed64(b)
	if n < 0 {
		return 0, protowire.ParseError(n)
	}
	p := *(**float64)(target)
	if p == nil {
		p = new(float64)
		*(**float64)(target) = p
	}
	*p = math.Float64frombits(v)
	return n, nil
}

func decodeOptString(target unsafe.Pointer, b []byte, opts Options) (int, error) {
	v, n := protowire.ConsumeBytes(b)
	if n < 0 {
		return 0, protowire.ParseError(n)
	}
	if !opts.AllowInvalidUTF8 && !utf8.Valid(v) {
		return 0, errInvalidUTF8
	}
	p := *(**string)(target)
	if p == nil {
		p = new(string)
		*(**string)(target) = p
	}
	if opts.AllowAlias {
		*p = unsafe.String(unsafe.SliceData(v), len(v))
	} else {
		*p = string(v)
	}
	return n, nil
}

// Repeated packed & unpacked slices.

func decodePackedInt32(target unsafe.Pointer, b []byte, _ Options) (int, error) {
	buf, n := protowire.ConsumeBytes(b)
	if n < 0 {
		return 0, protowire.ParseError(n)
	}
	s := *(*[]int32)(target)
	for len(buf) > 0 {
		v, vn := protowire.ConsumeVarint(buf)
		if vn < 0 {
			return 0, protowire.ParseError(vn)
		}
		s = append(s, int32(v))
		buf = buf[vn:]
	}
	*(*[]int32)(target) = s
	return n, nil
}

func decodeUnpackedInt32(target unsafe.Pointer, b []byte, _ Options) (int, error) {
	v, n := protowire.ConsumeVarint(b)
	if n < 0 {
		return 0, protowire.ParseError(n)
	}
	*(*[]int32)(target) = append(*(*[]int32)(target), int32(v))
	return n, nil
}

func decodePackedInt64(target unsafe.Pointer, b []byte, _ Options) (int, error) {
	buf, n := protowire.ConsumeBytes(b)
	if n < 0 {
		return 0, protowire.ParseError(n)
	}
	s := *(*[]int64)(target)
	for len(buf) > 0 {
		v, vn := protowire.ConsumeVarint(buf)
		if vn < 0 {
			return 0, protowire.ParseError(vn)
		}
		s = append(s, int64(v))
		buf = buf[vn:]
	}
	*(*[]int64)(target) = s
	return n, nil
}

func decodeUnpackedInt64(target unsafe.Pointer, b []byte, _ Options) (int, error) {
	v, n := protowire.ConsumeVarint(b)
	if n < 0 {
		return 0, protowire.ParseError(n)
	}
	*(*[]int64)(target) = append(*(*[]int64)(target), int64(v))
	return n, nil
}

func decodePackedUint32(target unsafe.Pointer, b []byte, _ Options) (int, error) {
	buf, n := protowire.ConsumeBytes(b)
	if n < 0 {
		return 0, protowire.ParseError(n)
	}
	s := *(*[]uint32)(target)
	for len(buf) > 0 {
		v, vn := protowire.ConsumeVarint(buf)
		if vn < 0 {
			return 0, protowire.ParseError(vn)
		}
		s = append(s, uint32(v))
		buf = buf[vn:]
	}
	*(*[]uint32)(target) = s
	return n, nil
}

func decodeUnpackedUint32(target unsafe.Pointer, b []byte, _ Options) (int, error) {
	v, n := protowire.ConsumeVarint(b)
	if n < 0 {
		return 0, protowire.ParseError(n)
	}
	*(*[]uint32)(target) = append(*(*[]uint32)(target), uint32(v))
	return n, nil
}

func decodePackedUint64(target unsafe.Pointer, b []byte, _ Options) (int, error) {
	buf, n := protowire.ConsumeBytes(b)
	if n < 0 {
		return 0, protowire.ParseError(n)
	}
	s := *(*[]uint64)(target)
	for len(buf) > 0 {
		v, vn := protowire.ConsumeVarint(buf)
		if vn < 0 {
			return 0, protowire.ParseError(vn)
		}
		s = append(s, v)
		buf = buf[vn:]
	}
	*(*[]uint64)(target) = s
	return n, nil
}

func decodeUnpackedUint64(target unsafe.Pointer, b []byte, _ Options) (int, error) {
	v, n := protowire.ConsumeVarint(b)
	if n < 0 {
		return 0, protowire.ParseError(n)
	}
	*(*[]uint64)(target) = append(*(*[]uint64)(target), v)
	return n, nil
}

func decodePackedSint32(target unsafe.Pointer, b []byte, _ Options) (int, error) {
	buf, n := protowire.ConsumeBytes(b)
	if n < 0 {
		return 0, protowire.ParseError(n)
	}
	s := *(*[]int32)(target)
	for len(buf) > 0 {
		v, vn := protowire.ConsumeVarint(buf)
		if vn < 0 {
			return 0, protowire.ParseError(vn)
		}
		s = append(s, int32(protowire.DecodeZigZag(v)))
		buf = buf[vn:]
	}
	*(*[]int32)(target) = s
	return n, nil
}

func decodeUnpackedSint32(target unsafe.Pointer, b []byte, _ Options) (int, error) {
	v, n := protowire.ConsumeVarint(b)
	if n < 0 {
		return 0, protowire.ParseError(n)
	}
	*(*[]int32)(target) = append(*(*[]int32)(target), int32(protowire.DecodeZigZag(v)))
	return n, nil
}

func decodePackedSint64(target unsafe.Pointer, b []byte, _ Options) (int, error) {
	buf, n := protowire.ConsumeBytes(b)
	if n < 0 {
		return 0, protowire.ParseError(n)
	}
	s := *(*[]int64)(target)
	for len(buf) > 0 {
		v, vn := protowire.ConsumeVarint(buf)
		if vn < 0 {
			return 0, protowire.ParseError(vn)
		}
		s = append(s, protowire.DecodeZigZag(v))
		buf = buf[vn:]
	}
	*(*[]int64)(target) = s
	return n, nil
}

func decodeUnpackedSint64(target unsafe.Pointer, b []byte, _ Options) (int, error) {
	v, n := protowire.ConsumeVarint(b)
	if n < 0 {
		return 0, protowire.ParseError(n)
	}
	*(*[]int64)(target) = append(*(*[]int64)(target), protowire.DecodeZigZag(v))
	return n, nil
}

func decodePackedBool(target unsafe.Pointer, b []byte, _ Options) (int, error) {
	buf, n := protowire.ConsumeBytes(b)
	if n < 0 {
		return 0, protowire.ParseError(n)
	}
	s := *(*[]bool)(target)
	for len(buf) > 0 {
		v, vn := protowire.ConsumeVarint(buf)
		if vn < 0 {
			return 0, protowire.ParseError(vn)
		}
		s = append(s, v != 0)
		buf = buf[vn:]
	}
	*(*[]bool)(target) = s
	return n, nil
}

func decodeUnpackedBool(target unsafe.Pointer, b []byte, _ Options) (int, error) {
	v, n := protowire.ConsumeVarint(b)
	if n < 0 {
		return 0, protowire.ParseError(n)
	}
	*(*[]bool)(target) = append(*(*[]bool)(target), v != 0)
	return n, nil
}

func decodePackedFixed32(target unsafe.Pointer, b []byte, _ Options) (int, error) {
	buf, n := protowire.ConsumeBytes(b)
	if n < 0 {
		return 0, protowire.ParseError(n)
	}
	count := len(buf) / 4
	s := slices.Grow(*(*[]uint32)(target), count)
	for len(buf) >= 4 {
		v, _ := protowire.ConsumeFixed32(buf)
		s = append(s, v)
		buf = buf[4:]
	}
	*(*[]uint32)(target) = s
	return n, nil
}

func decodeUnpackedFixed32(target unsafe.Pointer, b []byte, _ Options) (int, error) {
	v, n := protowire.ConsumeFixed32(b)
	if n < 0 {
		return 0, protowire.ParseError(n)
	}
	*(*[]uint32)(target) = append(*(*[]uint32)(target), v)
	return n, nil
}

func decodePackedSfixed32(target unsafe.Pointer, b []byte, _ Options) (int, error) {
	buf, n := protowire.ConsumeBytes(b)
	if n < 0 {
		return 0, protowire.ParseError(n)
	}
	count := len(buf) / 4
	s := slices.Grow(*(*[]int32)(target), count)
	for len(buf) >= 4 {
		v, _ := protowire.ConsumeFixed32(buf)
		s = append(s, int32(v))
		buf = buf[4:]
	}
	*(*[]int32)(target) = s
	return n, nil
}

func decodeUnpackedSfixed32(target unsafe.Pointer, b []byte, _ Options) (int, error) {
	v, n := protowire.ConsumeFixed32(b)
	if n < 0 {
		return 0, protowire.ParseError(n)
	}
	*(*[]int32)(target) = append(*(*[]int32)(target), int32(v))
	return n, nil
}

func decodePackedFloat32(target unsafe.Pointer, b []byte, _ Options) (int, error) {
	buf, n := protowire.ConsumeBytes(b)
	if n < 0 {
		return 0, protowire.ParseError(n)
	}
	count := len(buf) / 4
	s := slices.Grow(*(*[]float32)(target), count)
	for len(buf) >= 4 {
		v, _ := protowire.ConsumeFixed32(buf)
		s = append(s, math.Float32frombits(v))
		buf = buf[4:]
	}
	*(*[]float32)(target) = s
	return n, nil
}

func decodeUnpackedFloat32(target unsafe.Pointer, b []byte, _ Options) (int, error) {
	v, n := protowire.ConsumeFixed32(b)
	if n < 0 {
		return 0, protowire.ParseError(n)
	}
	*(*[]float32)(target) = append(*(*[]float32)(target), math.Float32frombits(v))
	return n, nil
}

func decodePackedFixed64(target unsafe.Pointer, b []byte, _ Options) (int, error) {
	buf, n := protowire.ConsumeBytes(b)
	if n < 0 {
		return 0, protowire.ParseError(n)
	}
	count := len(buf) / 8
	s := slices.Grow(*(*[]uint64)(target), count)
	for len(buf) >= 8 {
		v, _ := protowire.ConsumeFixed64(buf)
		s = append(s, v)
		buf = buf[8:]
	}
	*(*[]uint64)(target) = s
	return n, nil
}

func decodeUnpackedFixed64(target unsafe.Pointer, b []byte, _ Options) (int, error) {
	v, n := protowire.ConsumeFixed64(b)
	if n < 0 {
		return 0, protowire.ParseError(n)
	}
	*(*[]uint64)(target) = append(*(*[]uint64)(target), v)
	return n, nil
}

func decodePackedSfixed64(target unsafe.Pointer, b []byte, _ Options) (int, error) {
	buf, n := protowire.ConsumeBytes(b)
	if n < 0 {
		return 0, protowire.ParseError(n)
	}
	count := len(buf) / 8
	s := slices.Grow(*(*[]int64)(target), count)
	for len(buf) >= 8 {
		v, _ := protowire.ConsumeFixed64(buf)
		s = append(s, int64(v))
		buf = buf[8:]
	}
	*(*[]int64)(target) = s
	return n, nil
}

func decodeUnpackedSfixed64(target unsafe.Pointer, b []byte, _ Options) (int, error) {
	v, n := protowire.ConsumeFixed64(b)
	if n < 0 {
		return 0, protowire.ParseError(n)
	}
	*(*[]int64)(target) = append(*(*[]int64)(target), int64(v))
	return n, nil
}

func decodePackedFloat64(target unsafe.Pointer, b []byte, _ Options) (int, error) {
	buf, n := protowire.ConsumeBytes(b)
	if n < 0 {
		return 0, protowire.ParseError(n)
	}
	count := len(buf) / 8
	s := slices.Grow(*(*[]float64)(target), count)
	for len(buf) >= 8 {
		v, _ := protowire.ConsumeFixed64(buf)
		s = append(s, math.Float64frombits(v))
		buf = buf[8:]
	}
	*(*[]float64)(target) = s
	return n, nil
}

func decodeUnpackedFloat64(target unsafe.Pointer, b []byte, _ Options) (int, error) {
	v, n := protowire.ConsumeFixed64(b)
	if n < 0 {
		return 0, protowire.ParseError(n)
	}
	*(*[]float64)(target) = append(*(*[]float64)(target), math.Float64frombits(v))
	return n, nil
}

func decodeRepeatedString(target unsafe.Pointer, b []byte, opts Options) (int, error) {
	v, n := protowire.ConsumeBytes(b)
	if n < 0 {
		return 0, protowire.ParseError(n)
	}
	if !opts.AllowInvalidUTF8 && !utf8.Valid(v) {
		return 0, errInvalidUTF8
	}
	s := *(*[]string)(target)
	if opts.AllowAlias {
		s = append(s, unsafe.String(unsafe.SliceData(v), len(v)))
	} else {
		s = append(s, string(v))
	}
	*(*[]string)(target) = s
	return n, nil
}

func decodeRepeatedBytes(target unsafe.Pointer, b []byte, opts Options) (int, error) {
	v, n := protowire.ConsumeBytes(b)
	if n < 0 {
		return 0, protowire.ParseError(n)
	}
	s := *(*[][]byte)(target)
	if opts.AllowAlias {
		s = append(s, v)
	} else {
		s = append(s, bytes.Clone(v))
	}
	*(*[][]byte)(target) = s
	return n, nil
}

// Submessages.

type subMessageDecoder struct {
	subDec   *Decoder
	elemType reflect.Type
}

func (s *subMessageDecoder) decodeSingular(target unsafe.Pointer, b []byte, opts Options) (int, error) {
	buf, n := protowire.ConsumeBytes(b)
	if n < 0 {
		return 0, protowire.ParseError(n)
	}
	subPtr := *(*unsafe.Pointer)(target)
	if subPtr == nil {
		newSub := reflect.New(s.elemType).UnsafePointer()
		*(*unsafe.Pointer)(target) = newSub
		subPtr = newSub
	}
	subOpts := opts
	subOpts.MaxDepth--
	if err := s.subDec.Decode(subPtr, buf, subOpts); err != nil {
		return 0, err
	}
	return n, nil
}

func (s *subMessageDecoder) decodeRepeated(target unsafe.Pointer, b []byte, opts Options) (int, error) {
	buf, n := protowire.ConsumeBytes(b)
	if n < 0 {
		return 0, protowire.ParseError(n)
	}
	newSub := reflect.New(s.elemType).UnsafePointer()
	subOpts := opts
	subOpts.MaxDepth--
	if err := s.subDec.Decode(newSub, buf, subOpts); err != nil {
		return 0, err
	}
	*(*[]unsafe.Pointer)(target) = append(*(*[]unsafe.Pointer)(target), newSub)
	return n, nil
}

// Maps.

type mapEntryDecoder struct {
	mapType    reflect.Type
	keyDecoder func(b []byte, opts Options) (reflect.Value, int, error)
	valDecoder func(b []byte, opts Options) (reflect.Value, int, error)
	defKey     reflect.Value
	defVal     reflect.Value
}

func (m *mapEntryDecoder) decode(target unsafe.Pointer, b []byte, opts Options) (int, error) {
	buf, n := protowire.ConsumeBytes(b)
	if n < 0 {
		return 0, protowire.ParseError(n)
	}
	mapPtr := (*unsafe.Pointer)(target)
	var mapVal reflect.Value
	if *mapPtr == nil {
		mapVal = reflect.MakeMap(m.mapType)
		*mapPtr = unsafe.Pointer(mapVal.Pointer())
	} else {
		mapVal = reflect.NewAt(m.mapType, target).Elem()
	}

	key := m.defKey
	val := m.defVal

	for len(buf) > 0 {
		tag, tagLen := protowire.ConsumeVarint(buf)
		if tagLen < 0 {
			return 0, protowire.ParseError(tagLen)
		}
		buf = buf[tagLen:]

		fieldNum := protowire.Number(tag >> 3)
		wireType := protowire.Type(tag & 0b111)

		switch fieldNum {
		case 1:
			k, kn, err := m.keyDecoder(buf, opts)
			if err != nil {
				return 0, err
			}
			key = k
			buf = buf[kn:]
		case 2:
			v, vn, err := m.valDecoder(buf, opts)
			if err != nil {
				return 0, err
			}
			val = v
			buf = buf[vn:]
		default:
			skipLen := protowire.ConsumeFieldValue(fieldNum, wireType, buf)
			if skipLen < 0 {
				return 0, protowire.ParseError(skipLen)
			}
			buf = buf[skipLen:]
		}
	}

	mapVal.SetMapIndex(key, val)
	return n, nil
}

// Oneofs.

type oneofFieldDecoder struct {
	fieldIndex  []int        // index into parent struct for FieldByIndex
	parentType  reflect.Type // parent struct type
	wrapperType reflect.Type // *Oneof_S1 pointer type
	structType  reflect.Type // Oneof_S1 struct type
	innerOffset uintptr      // offset of field inside wrapper struct
	innerDecode decodeFunc
	isMessage   bool
	subDec      *subMessageDecoder
}

func (o *oneofFieldDecoder) decode(parentBase unsafe.Pointer, b []byte, opts Options) (int, error) {
	parentVal := reflect.NewAt(o.parentType, parentBase).Elem()
	fieldVal := parentVal.FieldByIndex(o.fieldIndex)

	if o.isMessage && !fieldVal.IsNil() && fieldVal.Elem().Type() == o.wrapperType {
		// Existing wrapper with submessage: merge
		wrapperPtr := fieldVal.Elem().UnsafePointer()
		innerTarget := unsafe.Add(wrapperPtr, o.innerOffset)
		return o.innerDecode(innerTarget, b, opts)
	}

	wrapperVal := reflect.New(o.structType)
	wrapperPtr := wrapperVal.UnsafePointer()
	innerTarget := unsafe.Add(wrapperPtr, o.innerOffset)

	n, err := o.innerDecode(innerTarget, b, opts)
	if err != nil {
		return 0, err
	}

	fieldVal.Set(wrapperVal)
	return n, nil
}
