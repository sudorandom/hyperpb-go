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

// Options configures wire decoding behavior for structdec.
type Options struct {
	// AllowAlias avoids copying string and bytes fields from the input buffer.
	AllowAlias bool

	// DiscardUnknown skips unknown fields instead of storing them into
	// the struct's unknownFields field.
	DiscardUnknown bool

	// AllowInvalidUTF8 skips UTF-8 validation on string fields.
	AllowInvalidUTF8 bool

	// MaxDepth limits the recursion depth for nested messages.
	MaxDepth int

	// AllowPartial allows unmarshaling messages with missing required fields.
	AllowPartial bool
}

// DefaultOptions returns the default Options with a reasonable MaxDepth.
func DefaultOptions() Options {
	return Options{
		MaxDepth: 1000,
	}
}
