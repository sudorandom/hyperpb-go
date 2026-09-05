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
	"fmt"
	"reflect"
	"unsafe"

	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"

	"buf.build/go/hyperpb/internal/xunsafe"
)

type fieldEntry struct {
	offset  uintptr
	isOneof bool
	decode  decodeFunc
}

type reqFieldInfo struct {
	offset uintptr
	name   string
}

// Decoder decodes protobuf wire format directly into a Go struct.
type Decoder struct {
	structType    reflect.Type
	lut           [128]uint8
	tagMap        map[uint64]uint16
	fields        []fieldEntry
	unknownOffset uintptr
	hasUnknown    bool
	reqFields     []reqFieldInfo
}

// DecodeMessage decodes binary protobuf wire format data into msg.
func (d *Decoder) DecodeMessage(msg proto.Message, b []byte, opts Options) error {
	if msg == nil {
		return errors.New("structdec: message is nil")
	}
	base := unsafe.Pointer(xunsafe.AnyData(msg))
	if base == nil {
		return errors.New("structdec: message pointer is nil")
	}
	return d.Decode(base, b, opts)
}

// Decode decodes binary protobuf wire format data directly into the struct at base.
func (d *Decoder) Decode(base unsafe.Pointer, b []byte, opts Options) error {
	if opts.MaxDepth <= 0 {
		return errRecursionDepth
	}
	for len(b) > 0 {
		var tag uint64
		var tagLen int
		if b[0] < 0x80 {
			tag = uint64(b[0])
			tagLen = 1
		} else {
			var n int
			tag, n = protowire.ConsumeVarint(b)
			if n < 0 {
				return protowire.ParseError(n)
			}
			tagLen = n
		}
		origTagBytes := b[:tagLen]
		b = b[tagLen:]

		var f *fieldEntry
		if tag < 128 {
			idx := d.lut[tag]
			if idx != 0xff {
				f = &d.fields[idx]
			}
		} else if len(d.tagMap) > 0 {
			if idx, ok := d.tagMap[tag]; ok {
				f = &d.fields[idx]
			}
		}

		if f == nil {
			fieldNum := protowire.Number(tag >> 3)
			wireType := protowire.Type(tag & 0b111)
			if fieldNum == 0 {
				return errInvalidFieldNum
			}
			valLen := protowire.ConsumeFieldValue(fieldNum, wireType, b)
			if valLen < 0 {
				return protowire.ParseError(valLen)
			}
			if !opts.DiscardUnknown && d.hasUnknown {
				uPtr := (*[]byte)(unsafe.Add(base, d.unknownOffset))
				*uPtr = append(*uPtr, origTagBytes...)
				*uPtr = append(*uPtr, b[:valLen]...)
			}
			b = b[valLen:]
			continue
		}

		var target unsafe.Pointer
		if f.isOneof {
			target = base
		} else {
			target = unsafe.Add(base, f.offset)
		}

		n, err := f.decode(target, b, opts)
		if err != nil {
			return err
		}
		b = b[n:]
	}

	if !opts.AllowPartial && len(d.reqFields) > 0 {
		for _, req := range d.reqFields {
			p := *(*unsafe.Pointer)(unsafe.Add(base, req.offset))
			if p == nil {
				return fmt.Errorf("%w: %s", errRequiredNotSet, req.name)
			}
		}
	}

	return nil
}
