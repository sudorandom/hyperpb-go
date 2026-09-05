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

package structenc

import (
	"errors"
)

var (
	// ErrRequiredNotSet is returned when a required field is not populated and AllowPartial is false.
	ErrRequiredNotSet = errors.New("structenc: required field not set")

	// ErrInvalidUTF8 is returned when a string field contains invalid UTF-8 and AllowInvalidUTF8 is false.
	ErrInvalidUTF8 = errors.New("structenc: string field contains invalid UTF-8")

	// ErrNilMessage is returned when a nil message is passed where non-nil is expected.
	ErrNilMessage = errors.New("structenc: nil message")
)
