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

package hyperpb

import (
	"google.golang.org/protobuf/reflect/protoreflect"

	"buf.build/go/hyperpb/internal/tdp"
	"buf.build/go/hyperpb/internal/tdp/dynamic"
	"buf.build/go/hyperpb/internal/xunsafe"
)

// Shared is state that is shared by all messages in a particular tree of
// messages.
//
// The zero value is ready to use: construct it with new(Shared).
type Shared struct {
	impl dynamic.Shared
}

// NewMessage allocates a new message using this value's resources.
func (s *Shared) NewMessage(msgType *MessageType) *Message {
	if s == nil {
		s = new(Shared)
	}

	// Previously, this code was here:
	//
	// // Easy mistake to make: the memory allocated in alloc() contains no
	// // pointers, so even though msgType is "reachable" through m, it's not reachable
	// // from the GC's perspective, so we need to mark it as alive here.
	// //
	// // This implicitly marks all other types reachable from msgType as alive, meaning
	// // we only need to do this for top-level calls to New().
	// c.arena.KeepAlive(msgType)
	//
	// It is now redundant, because Context stores msgType.Library(). The comment is
	// kept for posterity about a nasty bug.

	return wrapMessage(s.impl.New(&msgType.impl))
}

// Free releases any resources held by this value, allowing them to be re-used.
//
// Any messages previously parsed using this value must not be reused.
func (s *Shared) Free() { s.impl.Free() }

// wrapShared wraps an internal Shared pointer.
func wrapShared(s *dynamic.Shared) *Shared {
	return xunsafe.Cast[Shared](s)
}

func init() {
	dynamic.CompileHook = func(md protoreflect.MessageDescriptor) *tdp.Type {
		return &CompileMessageDescriptor(md).impl
	}
}
