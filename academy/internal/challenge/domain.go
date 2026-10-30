// Package challenge runs coding challenges (content-model.md "Challenge", kind code): the learner's function, in
// one of the challenge's languages, is called on each case by a judge (harness/) in a program step of an OpsForge
// sandbox; Academy compares what it returned with the case's answer, which never leaves the platform. Run tries the
// sample cases; Submit tries every case and records a submission (accepted earns the challenge's XP once).
package challenge

import (
	"bytes"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"
	"unicode"
)

// Errors of the module; the transport maps each to a problem.
var (
	ErrNotACodeChallenge = errors.New("this item is not a coding challenge")
	ErrLanguage          = errors.New("the challenge has no such language")
	ErrCodeTooLarge      = errors.New("the code is too large")
	ErrSolutionLocked    = errors.New("the solution opens once a submission is accepted")
	ErrBusy              = errors.New("the lab is busy")
	ErrLabDown           = errors.New("the lab is down")
	ErrUnavailable       = errors.New("the running lab cannot run this challenge")
	ErrOffline           = errors.New("OpsForge is out of reach")
)

// MaxCode is the largest solution accepted (bytes).
const MaxCode = 64 * 1024

// Mark starts a judge's line about a case (harness/): `\x1eACADEMY {"i":0,"got":...,"ms":0.1}` or
// `{"compile":"..."}`. Every other line is the program's own output.
const Mark = "\x1eACADEMY "

//go:embed harness
var harnesses embed.FS

// Language is how one language's solution is judged: the learner's file, the judge's file and the command running
// both (in the sandbox's workspace), and how a challenge's function is named in it.
type Language struct {
	Runtime, File, Judge string
	Cmd                  []string
	Name                 func(function string) string
}

// Languages are the languages a challenge may offer, by name (OpsForge's program runtimes).
var Languages = map[string]Language{
	"python": {Runtime: "python", File: "solution.py", Judge: "judge.py", Cmd: []string{"python3", "judge.py"},
		Name: snake},
	"javascript": {Runtime: "javascript", File: "solution.js", Judge: "judge.js", Cmd: []string{"node", "judge.js"},
		Name: func(f string) string { return f }},
	"go": {Runtime: "go", File: "solution.go", Judge: "main.go",
		Cmd: []string{"go", "run", "main.go", "solution.go"}, Name: exported},
}

// snake is a camelCase name in snake_case (twoSum → two_sum).
func snake(name string) string {
	var b strings.Builder
	for i, r := range name {
		if unicode.IsUpper(r) {
			if i > 0 {
				b.WriteByte('_')
			}
			r = unicode.ToLower(r)
		}
		b.WriteRune(r)
	}
	return b.String()
}

// exported is a camelCase name with its first letter upper case (twoSum → TwoSum).
func exported(name string) string {
	if name == "" {
		return name
	}
	return strings.ToUpper(name[:1]) + name[1:]
}

// Case is one test: the function's arguments and the answer it must return.
type Case struct {
	Args []json.RawMessage `json:"args"`
	Want json.RawMessage   `json:"want"`
}

// Def is a coding challenge as the bundle holds it. Compare is "exact", or "unordered" (the answer is a list whose
// order does not matter); the first Samples cases are the examples (shown, and what Run tries).
type Def struct {
	Kind       string            `json:"kind"`
	Function   string            `json:"function"`
	Params     []string          `json:"params"`
	Compare    string            `json:"compare"`
	Samples    int               `json:"samples"`
	Cases      []Case            `json:"cases"`
	Languages  []string          `json:"languages"`
	Solution   map[string]string `json:"solution"`
	TimeLimitS int               `json:"time_limit_s"`
	XP         int               `json:"xp"`
}

// Decode reads a challenge entry; ErrNotACodeChallenge for a manifest challenge.
func Decode(doc []byte) (Def, error) {
	var d Def
	if err := json.Unmarshal(doc, &d); err != nil {
		return d, fmt.Errorf("challenge: %w", err)
	}
	if d.Kind != "code" {
		return d, ErrNotACodeChallenge
	}
	return d, nil
}

// Language is the definition of one of the challenge's languages, or ErrLanguage.
func (d Def) Language(name string) (Language, error) {
	l, ok := Languages[name]
	if !ok || !slices.Contains(d.Languages, name) {
		return Language{}, fmt.Errorf("%w: %q (one of %s)", ErrLanguage, name, strings.Join(d.Languages, ", "))
	}
	return l, nil
}

// Files are the sandbox's workspace for judging code in lang on cases: the learner's file, the judge and the cases'
// arguments (never their answers).
func Files(d Def, lang Language, code string, cases []Case) (map[string]string, error) {
	src := lang.Judge
	if strings.HasSuffix(src, ".go") {
		src += ".tmpl" // not compiled with this package
	}
	judge, err := harnesses.ReadFile("harness/" + src)
	if err != nil {
		return nil, err
	}
	name := lang.Name(d.Function)
	text := strings.ReplaceAll(string(judge), "{{FUNCTION}}", name)
	args := make([]map[string]any, len(cases))
	for i, c := range cases {
		args[i] = map[string]any{"args": c.Args}
	}
	tests, err := json.Marshal(map[string]any{"function": name, "cases": args})
	if err != nil {
		return nil, err
	}
	return map[string]string{lang.File: code, lang.Judge: text, "tests.json": string(tests)}, nil
}

// Steps are the action judging the files: one program step named "judge".
func Steps(lang Language, d Def) ([]json.RawMessage, error) {
	files := []string{lang.Judge, lang.File, "tests.json"}
	step, err := json.Marshal(map[string]any{"name": "judge", "program": map[string]any{
		"runtime": lang.Runtime, "files": files, "cmd": lang.Cmd, "timeout": fmt.Sprintf("%ds", d.TimeLimitS)}})
	return []json.RawMessage{step}, err
}

// Verdicts, LeetCode's: the first failing case decides.
const (
	Accepted          = "accepted"
	WrongAnswer       = "wrong_answer"
	RuntimeError      = "runtime_error"
	CompileError      = "compile_error"
	TimeLimitExceeded = "time_limit_exceeded"
	MemoryExceeded    = "memory_limit_exceeded"
	Error             = "error" // OpsForge could not run it: not the learner's fault, not counted
)

// CaseResult is one case's outcome. Got is what the function returned (absent after an error or when the program
// never reached the case); MS its time in the judge.
type CaseResult struct {
	N      int               `json:"n"`
	Passed bool              `json:"passed"`
	Args   []json.RawMessage `json:"args"`
	Want   json.RawMessage   `json:"want"`
	Got    json.RawMessage   `json:"got,omitempty"`
	Error  string            `json:"error,omitempty"`
	MS     *float64          `json:"ms,omitempty"`
}

// Result is a run's or a submission's outcome. Cases are every case for a run, and for a submission only the first
// failing one (the others' arguments stay hidden; Passed counts the cases before it); Output is what the program printed itself (its stdout and stderr, compiler errors
// included); Seconds the program's time.
type Result struct {
	Verdict  string       `json:"verdict"`
	Passed   int          `json:"passed"`
	Total    int          `json:"total"`
	Cases    []CaseResult `json:"cases"`
	Output   string       `json:"output"`
	Seconds  float64      `json:"seconds"`
	Error    string       `json:"error,omitempty"`
	Language string       `json:"language"`
}

// record is one marked line of the judge.
type record struct {
	I       *int            `json:"i"`
	Got     json.RawMessage `json:"got"`
	Error   *string         `json:"error"`
	Compile *string         `json:"compile"`
	MS      *float64        `json:"ms"`
}

// parse splits a judge's output into its records by case, a compile error, and the program's own output.
func parse(out string) (map[int]record, *string, string) {
	byCase := map[int]record{}
	var compile *string
	var own []string
	for _, line := range strings.Split(out, "\n") {
		if rest, ok := strings.CutPrefix(line, Mark); ok {
			var r record
			if json.Unmarshal([]byte(rest), &r) == nil {
				switch {
				case r.Compile != nil:
					compile = r.Compile
				case r.I != nil:
					byCase[*r.I] = r
				}
				continue
			}
		}
		own = append(own, line)
	}
	return byCase, compile, strings.Trim(strings.Join(own, "\n"), "\n")
}

// Run is what the judge's program step reported: its metrics (exit, timed_out, oom, seconds) and output.
type Run struct {
	Exit, Seconds   float64
	TimedOut, OOM   bool
	Output          string
	Failure         string // OpsForge's error: the step did not run
	StopAtFirstFail bool   // a submission: it stops at the first failing case and shows only that one
}

// Judge turns a judge's run on cases into the verdict, comparing each result with its answer.
func Judge(d Def, cases []Case, run Run) Result {
	res := Result{Total: len(cases), Cases: []CaseResult{}, Seconds: run.Seconds}
	if run.Failure != "" {
		res.Verdict, res.Error = Error, run.Failure
		return res
	}
	byCase, compile, own := parse(run.Output)
	res.Output = own
	if compile != nil {
		res.Verdict, res.Output = CompileError, strings.TrimSpace(strings.Join([]string{own, *compile}, "\n"))
		return res
	}
	if len(byCase) == 0 && !run.TimedOut && !run.OOM && run.Exit != 0 {
		res.Verdict = CompileError // the program never started its cases: a compiler's errors are in Output
		return res
	}
	for i, c := range cases {
		cr := CaseResult{N: i + 1, Args: c.Args, Want: c.Want}
		r, ok := byCase[i]
		switch {
		case ok && r.Error != nil:
			cr.Error, cr.MS = *r.Error, r.MS
		case ok:
			cr.Got, cr.MS = r.Got, r.MS
			cr.Passed = Same(c.Want, r.Got, d.Compare)
		}
		if cr.Passed {
			res.Passed++
		} else {
			verdict := WrongAnswer
			switch {
			case ok && r.Error != nil:
				verdict = RuntimeError
			case ok:
			case run.OOM:
				verdict, cr.Error = MemoryExceeded, "the program ran out of memory"
			case run.TimedOut:
				verdict, cr.Error = TimeLimitExceeded, fmt.Sprintf("the time limit (%d s) ran out", d.TimeLimitS)
			default:
				verdict, cr.Error = RuntimeError, "the program stopped before this case"
			}
			if res.Verdict == "" {
				res.Verdict = verdict
			}
		}
		if !run.StopAtFirstFail {
			res.Cases = append(res.Cases, cr)
		} else if !cr.Passed {
			res.Cases = append(res.Cases, cr)
			break
		}
	}
	if res.Verdict == "" {
		res.Verdict = Accepted
	}
	return res
}

// Same reports whether got is the answer want: equal JSON values (numbers by value), or for "unordered" equal lists
// once sorted.
func Same(want, got json.RawMessage, compare string) bool {
	w, errW := canonical(want)
	g, errG := canonical(got)
	if errW != nil || errG != nil {
		return false
	}
	if compare == "unordered" {
		wl, okW := w.([]any)
		gl, okG := g.([]any)
		if okW && okG {
			return equalJSON(sorted(wl), sorted(gl))
		}
	}
	return equalJSON(w, g)
}

func canonical(raw json.RawMessage) (any, error) {
	if len(raw) == 0 {
		return nil, errors.New("no value")
	}
	var v any
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	return normalize(v), nil
}

// normalize turns numbers into float64 (1 and 1.0 are the same answer).
func normalize(v any) any {
	switch x := v.(type) {
	case json.Number:
		f, err := x.Float64()
		if err != nil {
			return x.String()
		}
		return f
	case []any:
		for i := range x {
			x[i] = normalize(x[i])
		}
	case map[string]any:
		for k := range x {
			x[k] = normalize(x[k])
		}
	}
	return v
}

func equalJSON(a, b any) bool {
	ja, _ := json.Marshal(a)
	jb, _ := json.Marshal(b)
	return bytes.Equal(ja, jb)
}

func sorted(l []any) []any {
	keys := make([]string, len(l))
	byKey := map[string][]any{}
	for i, v := range l {
		k, _ := json.Marshal(v)
		keys[i] = string(k)
		byKey[keys[i]] = append(byKey[keys[i]], v)
	}
	sort.Strings(keys)
	out := make([]any, len(l))
	for i, k := range keys {
		out[i] = byKey[k][0]
		byKey[k] = byKey[k][1:]
	}
	return out
}

// Submission is one submitted solution and its verdict. XP is what it earned: the challenge's XP on the first
// accepted submission, 0 otherwise.
type Submission struct {
	ID          int64
	UserID      int64
	Course      string
	Item        string
	Hash        string
	Language    string
	Code        string
	Verdict     string
	Passed      int
	Total       int
	Seconds     float64
	XP          int
	Result      Result
	SubmittedAt time.Time
}
