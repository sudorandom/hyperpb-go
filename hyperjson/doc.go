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
// [Unmarshal] parses protojson into a freshly allocated hyperpb message.
// By default, it writes directly into the hyperpb arena using a compiled
// per-type plan for maximum throughput and zero-allocation parsing. If direct
// writing is unavailable (for example, on custom well-known types or
// uncompiled extensions), it falls back to a descriptor-guided JSON-to-wire
// transcoder that feeds hyperpb's wire-format parser.
//
// # Unsupported protojson options
//
// EmitUnpopulated/EmitDefaultValues, Multiline/Indent, and AllowPartial are
// not yet implemented.
package hyperjson
