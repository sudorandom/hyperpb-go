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

package xunsafe_test

import (
	"testing"
	"unsafe"

	"github.com/stretchr/testify/assert"

	"buf.build/go/hyperpb/internal/xunsafe"
)

func TestIndirect(t *testing.T) {
	t.Parallel()

	assert.False(t, xunsafe.IsDirect[int]())
	assert.False(t, xunsafe.IsDirect[string]())
	assert.False(t, xunsafe.IsDirect[[]byte]())

	assert.True(t, xunsafe.IsDirect[*int]())
	assert.True(t, xunsafe.IsDirect[[1]*int]())
	assert.True(t, xunsafe.IsDirect[any]())
	assert.True(t, xunsafe.IsDirect[map[int]int]())
	assert.True(t, xunsafe.IsDirect[chan int]())
	assert.True(t, xunsafe.IsDirect[unsafe.Pointer]())
	assert.True(t, xunsafe.IsDirect[struct{ _ *int }]())
	assert.True(t, xunsafe.IsDirect[*struct{ _ *int }]())
}

func TestAnyBytes(t *testing.T) {
	t.Parallel()

	i := 0xaaaa
	p := &i
	assert.False(t, xunsafe.IsDirectAny(i))
	assert.True(t, xunsafe.IsDirectAny(p))

	assert.Equal(t, xunsafe.Bytes(&i), xunsafe.AnyBytes(i))
	assert.Equal(t, xunsafe.Bytes(&p), xunsafe.AnyBytes(p))

	p2 := struct{ p *int }{p}
	assert.Equal(t, xunsafe.Bytes(&p2), xunsafe.AnyBytes(p2))
}

func TestPC(t *testing.T) {
	t.Parallel()

	f := func() int { return 42 }
	pc := xunsafe.NewPC(f)

	t.Logf("%#x\n", pc)
	assert.Equal(t, 42, pc.Get()())
}

// AssertInlinedAny is a helper for testing that T does not allocate when
// converted to an interface.
func AssertInlinedAny[T any](t testing.TB) {
	t.Helper()
	var z T
	assert.True(t, xunsafe.IsDirect[T](), "expected %T to be pointer-shaped", z)
}

