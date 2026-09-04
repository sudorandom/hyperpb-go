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

package hyperjson_test

import (
	"fmt"
	"math/rand/v2"
	"os"
	"testing"
	"time"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/dynamicpb"

	"buf.build/go/hyperpb"
	"buf.build/go/hyperpb/hyperjson"
)

// TestStressDifferential is a long-running randomized differential tester,
// used because Go native fuzzing currently cannot instrument hyperpb (the
// //go:nosplit parser VM exceeds the nosplit stack budget once libfuzzer
// hooks are inserted). It mutates the seed corpus and checks the same
// invariants as the Fuzz* targets. Duration is set by HYPERJSON_STRESS, e.g.:
//
//	HYPERJSON_STRESS=8h go test ./hyperjson/ -run TestStressDifferential -v
//
// Every failure report includes the PRNG seed; rerun with HYPERJSON_SEED to
// reproduce.
func TestStressDifferential(t *testing.T) {
	durStr := os.Getenv("HYPERJSON_STRESS")
	if durStr == "" {
		durStr = "2s" // Smoke-test duration for normal test runs.
	}
	dur, err := time.ParseDuration(durStr)
	if err != nil {
		t.Fatalf("bad HYPERJSON_STRESS: %v", err)
	}

	var seed uint64
	if s := os.Getenv("HYPERJSON_SEED"); s != "" {
		if _, err := fmt.Sscanf(s, "%d", &seed); err != nil {
			t.Fatalf("bad HYPERJSON_SEED: %v", err)
		}
	} else {
		seed = rand.Uint64()
	}
	rng := rand.New(rand.NewPCG(seed, 0x68797065726a736e)) // "hyperjsn"

	deadline := time.Now().Add(dur)
	var n int
	for time.Now().Before(deadline) {
		n++
		sel := byte(rng.IntN(256))
		doc := mutateDoc(rng, fuzzSeeds[rng.IntN(len(fuzzSeeds))])
		if msg := checkDifferential(sel, doc); msg != "" {
			t.Fatalf("iteration %d (seed %d, sel %d):\n%s\ninput: %q", n, seed, sel, msg, doc)
		}
	}
	t.Logf("ran %d iterations over %v (seed %d)", n, dur, seed)
}

// mutateDoc applies a few random mutations to a seed document.
func mutateDoc(rng *rand.Rand, seed string) []byte {
	doc := []byte(seed)
	for range 1 + rng.IntN(4) {
		if len(doc) == 0 {
			break
		}
		switch rng.IntN(6) {
		case 0: // Truncate.
			doc = doc[:rng.IntN(len(doc))]
		case 1: // Flip a byte.
			doc[rng.IntN(len(doc))] = byte(rng.IntN(256))
		case 2: // Insert a structural token.
			toks := []string{"{", "}", "[", "]", ",", ":", `"`, "null", "-", "0", "1e999",
				`"A"`, `"\ud800"`, " ", `"NaN"`, "9223372036854775808", "true"}
			tok := toks[rng.IntN(len(toks))]
			i := rng.IntN(len(doc))
			doc = append(doc[:i:i], append([]byte(tok), doc[i:]...)...)
		case 3: // Duplicate a span.
			i := rng.IntN(len(doc))
			j := i + rng.IntN(len(doc)-i)
			doc = append(doc[:j:j], append(append([]byte{}, doc[i:j]...), doc[j:]...)...)
		case 4: // Delete a span.
			i := rng.IntN(len(doc))
			j := i + rng.IntN(len(doc)-i)
			doc = append(doc[:i:i], doc[j:]...)
		case 5: // Splice in another seed.
			other := fuzzSeeds[rng.IntN(len(fuzzSeeds))]
			i := rng.IntN(len(doc))
			doc = append(doc[:i:i], append([]byte(other), doc[i:]...)...)
		}
	}
	return doc
}

// checkDifferential runs the invariants shared with the Fuzz* targets,
// returning a description of any violation.
func checkDifferential(sel byte, data []byte) string {
	ct, md := fuzzType(sel)

	direct := hyperpb.NewMessage(ct)
	errD := hyperjson.Unmarshal(data, direct)
	trans := hyperpb.NewMessage(ct)
	errT := hyperjson.TranscodeUnmarshal(data, trans)

	if (errD == nil) != (errT == nil) {
		return fmt.Sprintf("direct/transcode error divergence:\n  direct:    %v\n  transcode: %v", errD, errT)
	}

	// The oracle only checks agreement when both parsers accept: accept/
	// reject divergence is expected in both directions (protojson leniently
	// accepts quoted numbers with trailing garbage like "-2,3", which the
	// conformance suite requires rejecting; hyperjson accepts duplicate map
	// keys last-wins, which protojson rejects).
	oracle := dynamicpb.NewMessage(md)
	errO := protojson.Unmarshal(data, oracle)
	if errD != nil {
		return ""
	}
	if !proto.Equal(direct, trans) {
		return "direct/transcode message divergence"
	}
	if errO == nil && !proto.Equal(oracle, direct) {
		return fmt.Sprintf("parse disagreement with protojson:\n  protojson: %s\n  hyperjson: %s",
			mustJSON(oracle), mustJSON(direct))
	}

	out, err := hyperjson.Marshal(direct)
	if err != nil {
		return fmt.Sprintf("marshal of parsed message failed: %v", err)
	}
	back := hyperpb.NewMessage(ct)
	if err := hyperjson.Unmarshal(out, back); err != nil {
		return fmt.Sprintf("round trip re-parse failed: %v (emitted %q)", err, out)
	}
	if !proto.Equal(direct, back) {
		return fmt.Sprintf("round trip inequality (emitted %q)", out)
	}
	return ""
}
