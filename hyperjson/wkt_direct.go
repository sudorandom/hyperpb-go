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

package hyperjson

import (
	"strings"
	"unsafe"

	"buf.build/go/hyperpb/internal/tdp/dynamic"
	"buf.build/go/hyperpb/internal/tdp/repeated"
)

// wktValue parses the custom JSON shape of a well-known type directly into
// its message storage. Because every WKT is structurally made of ordinary
// fields, most cases delegate to the generic field writers on the WKT's own
// plan.
func (w *writer) wktValue(p *dplan, m *dynamic.Message) error {
	switch p.wkt {
	case wkTimestamp:
		s, err := w.d.readString()
		if err != nil {
			return err
		}
		seconds, nanos, err := parseTimestamp(unsafeString(s))
		if err != nil {
			return w.d.errf("%v", err)
		}
		store(m, &p.byIdx[0], seconds)
		store(m, &p.byIdx[1], nanos)
		return nil

	case wkDuration:
		s, err := w.d.readString()
		if err != nil {
			return err
		}
		seconds, nanos, err := parseDuration(unsafeString(s))
		if err != nil {
			return w.d.errf("%v", err)
		}
		store(m, &p.byIdx[0], seconds)
		store(m, &p.byIdx[1], nanos)
		return nil

	case wkEmpty:
		return w.d.walkObject(func(key []byte) error {
			if w.opts.DiscardUnknown {
				return w.d.skipValue()
			}
			return w.d.errf("unknown field %q in google.protobuf.Empty", key)
		})

	case wkStruct:
		// A Struct's JSON object is exactly its fields map's JSON object.
		return w.mapField(&p.byIdx[0], m)

	case wkValue:
		// Pick the oneof member from the JSON value's kind; the generic
		// singular writer consumes the token and sets the which-word.
		// Fields: null(0), number(1), string(2), bool(3), struct(4), list(5).
		c, err := w.d.peek()
		if err != nil {
			return err
		}
		var idx int
		switch c {
		case 'n':
			idx = 0
		case 't', 'f':
			idx = 3
		case '"':
			idx = 2
		case '{':
			idx = 4
		case '[':
			idx = 5
		default:
			idx = 1
		}
		return w.singular(&p.byIdx[idx], m)

	case wkListValue:
		return w.listField(&p.byIdx[0], m)

	case wkFieldMask:
		s, err := w.d.readString()
		if err != nil {
			return err
		}
		if len(s) == 0 {
			return nil
		}
		df := &p.byIdx[0]
		strs := (*repeated.Strings)(fieldPtr(m, df.offset))
		for path := range strings.SplitSeq(string(s), ",") {
			snake, ok := fieldMaskPathToSnake(path)
			if !ok {
				return w.d.errf("invalid google.protobuf.FieldMask path %q", path)
			}
			if strs.Raw.Len() == 0 {
				w.fixups = append(w.fixups, unsafe.Pointer(&strs.Src))
			}
			strs.Raw = strs.Raw.AppendOne(w.arena, w.append([]byte(snake)))
		}
		return nil

	case wkAny:
		return w.anyValue(p, m)

	default: // wkWrapper
		return w.singular(p.wrapped, m)
	}
}

// anyValue parses a google.protobuf.Any: the payload is transcoded to wire
// format into the appendix, since the value field is bytes.
func (w *writer) anyValue(p *dplan, m *dynamic.Message) error {
	typeURL, payload, err := transcodeAnyPayload(&w.d, w.opts)
	if err != nil {
		return err
	}
	if typeURL == nil {
		return nil // Empty Any.
	}
	store(m, &p.byIdx[0], w.append(typeURL))
	store(m, &p.byIdx[1], w.append(payload))
	return nil
}
