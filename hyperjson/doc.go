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

// Package hyperjson is a proof-of-concept protojson codec for hyperpb
// messages.
//
// # Status
//
// This package is experimental. It implements the protojson wire format
// (JSON names, 64-bit integers as strings, well-known type shapes, etc.) for
// [hyperpb.Message] values without routing through the generic
// reflection-based protojson package.
//
// [Marshal] serializes a parsed hyperpb message by walking its compiled tdp
// field tables directly, reading values through the per-field getter thunks.
//
// [Unmarshal] parses protojson into a freshly allocated hyperpb message. In
// this proof of concept it is implemented as a descriptor-guided JSON-to-wire
// transcoder: the JSON input is converted to protobuf wire format and handed
// to hyperpb's wire parser, which reuses the arena, zero-copy string storage,
// and profile-guided layout machinery unchanged. A future version may write
// arena storage directly, which requires tdp layout support for strings that
// do not alias the parse source.
//
// # Unsupported protojson options
//
// EmitUnpopulated/EmitDefaultValues, Multiline/Indent, and AllowPartial are
// not yet implemented.
package hyperjson
