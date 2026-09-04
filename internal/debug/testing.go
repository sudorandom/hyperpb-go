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

//go:build debug

package debug


import (
	"testing"

	"github.com/timandy/routine"
)

var tls = routine.NewThreadLocal[testing.TB]()

// WithTesting sets a testing pointer for debugging.
//
// This will cause t.Log() to be used to print debug traces instead of Debug.
func WithTesting(t testing.TB) func() {
	t.Helper()

	prev := tls.Get()
	tls.Set(t)
	return func() {
		tls.Set(prev)
	}
}
