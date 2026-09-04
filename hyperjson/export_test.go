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
	"buf.build/go/hyperpb"
)

// TranscodeUnmarshal forces the JSON-to-wire transcode path, for
// differential testing against the direct writer.
func TranscodeUnmarshal(data []byte, msg *hyperpb.Message) error {
	return UnmarshalOptions{}.transcodeUnmarshal(data, msg)
}

// IsDirect reports whether the message's type takes the direct-write path.
func IsDirect(msg *hyperpb.Message) bool {
	return dplanFor(msg.Unwrap().Type()).direct
}
