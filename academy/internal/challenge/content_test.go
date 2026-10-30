package challenge_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/IhebBelhadj/opsforge-academy/academy/internal/challenge"
	"github.com/IhebBelhadj/opsforge-academy/academy/internal/compiler"
	"github.com/IhebBelhadj/opsforge-academy/academy/internal/platform"
)

// TestThePacksCodingChallenges compiles the real packs (../labs) and judges every coding challenge's reference
// solution, in each of its languages, on every case with the host's interpreters (skipped when one is missing): the
// cases and the solutions agree, and the starter code is not accepted.
func TestThePacksCodingChallenges(t *testing.T) {
	bundle, err := compiler.NewService("../../../labs", nil, platform.SystemClock{}).CompileUnchecked(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Errors []any
		Packs  []struct {
			Challenges []json.RawMessage
		}
	}
	if err := json.Unmarshal(bundle, &doc); err != nil || len(doc.Errors) > 0 {
		t.Fatalf("%v %v", doc.Errors, err)
	}
	n := 0
	for _, p := range doc.Packs {
		for _, raw := range p.Challenges {
			var meta struct{ ID string }
			_ = json.Unmarshal(raw, &meta)
			var starter struct{ Starter map[string]string }
			_ = json.Unmarshal(raw, &starter)
			d, err := challenge.Decode(raw)
			if err != nil {
				continue // a manifest challenge
			}
			n++
			for _, lang := range d.Languages {
				t.Run(meta.ID+"/"+lang, func(t *testing.T) {
					file := challenge.Languages[lang].File
					if res := judgeLocally(t, d, lang, d.Solution[file]); res.Verdict != challenge.Accepted {
						t.Fatalf("the solution: %+v", res)
					}
					if res := judgeLocally(t, d, lang, starter.Starter[file]); res.Verdict == challenge.Accepted {
						t.Fatalf("the starter code is accepted: %+v", res)
					}
				})
			}
		}
	}
	if n < 3 {
		t.Fatalf("%d coding challenges", n)
	}
}
