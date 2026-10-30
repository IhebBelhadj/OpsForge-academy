package challenge_test

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/IhebBelhadj/opsforge-academy/academy/internal/challenge"
)

func raw(s string) json.RawMessage { return json.RawMessage(s) }

// twoSum is a coding challenge as the bundle holds it: two samples, three cases, any order of the two indices.
func twoSum() challenge.Def {
	return challenge.Def{Kind: "code", Function: "twoSum", Params: []string{"nums", "target"}, Compare: "unordered",
		Samples: 2, TimeLimitS: 10, XP: 50, Languages: []string{"python", "javascript", "go"},
		Cases: []challenge.Case{
			{Args: []json.RawMessage{raw("[2,7,11,15]"), raw("9")}, Want: raw("[0,1]")},
			{Args: []json.RawMessage{raw("[3,2,4]"), raw("6")}, Want: raw("[1,2]")},
			{Args: []json.RawMessage{raw("[3,3]"), raw("6")}, Want: raw("[0,1]")},
		}}
}

func line(s string) string { return "\n" + challenge.Mark + s + "\n" }

func TestDecodeRefusesAManifestChallenge(t *testing.T) {
	if _, err := challenge.Decode([]byte(`{"files":["web.yaml"]}`)); !errors.Is(err, challenge.ErrNotACodeChallenge) {
		t.Fatal(err)
	}
	d, err := challenge.Decode([]byte(`{"kind":"code","function":"f","languages":["go"]}`))
	if err != nil || d.Function != "f" {
		t.Fatalf("%+v %v", d, err)
	}
	if _, err := d.Language("python"); !errors.Is(err, challenge.ErrLanguage) {
		t.Fatal(err)
	}
}

func TestFunctionNamesFollowEachLanguage(t *testing.T) {
	for lang, want := range map[string]string{"python": "two_sum", "javascript": "twoSum", "go": "TwoSum"} {
		if got := challenge.Languages[lang].Name("twoSum"); got != want {
			t.Errorf("%s: %s", lang, got)
		}
	}
}

func TestTheSandboxGetsTheArgumentsNeverTheAnswers(t *testing.T) {
	d := twoSum()
	files, err := challenge.Files(d, challenge.Languages["go"], "package main", d.Cases[:1])
	if err != nil {
		t.Fatal(err)
	}
	if files["solution.go"] != "package main" || !strings.Contains(files["main.go"], "var academyTarget = TwoSum") {
		t.Fatalf("%v", files)
	}
	if files["tests.json"] != `{"cases":[{"args":[[2,7,11,15],9]}],"function":"TwoSum"}` {
		t.Fatal(files["tests.json"])
	}
	steps, err := challenge.Steps(challenge.Languages["python"], d)
	if err != nil || string(steps[0]) != `{"name":"judge","program":{"cmd":["python3","judge.py"],`+
		`"files":["judge.py","solution.py","tests.json"],"runtime":"python","timeout":"10s"}}` {
		t.Fatalf("%s %v", steps, err)
	}
}

func TestSameComparesValuesAndUnorderedLists(t *testing.T) {
	for _, c := range []struct {
		want, got, compare string
		same               bool
	}{
		{"[0,1]", "[0, 1]", "exact", true},
		{"[0,1]", "[1,0]", "exact", false},
		{"[0,1]", "[1,0]", "unordered", true},
		{"[[1,6],[8,10]]", "[[8,10],[1,6]]", "unordered", true},
		{"1", "1.0", "exact", true},
		{"true", "1", "exact", false},
		{`{"a":[1]}`, `{"a": [1]}`, "exact", true},
		{"[1,1,2]", "[1,2,2]", "unordered", false},
		{"[0,1]", "", "exact", false},
	} {
		if got := challenge.Same(raw(c.want), raw(c.got), c.compare); got != c.same {
			t.Errorf("%s vs %s (%s): %v", c.want, c.got, c.compare, got)
		}
	}
}

func TestJudgeVerdicts(t *testing.T) {
	d := twoSum()
	all := line(`{"i":0,"got":[1,0],"ms":0.1}`) + line(`{"i":1,"got":[1,2],"ms":0.1}`) +
		line(`{"i":2,"got":[0,1],"ms":0.1}`)
	for _, c := range []struct {
		name    string
		run     challenge.Run
		verdict string
		passed  int
		cases   int
	}{
		{"every case right", challenge.Run{Output: "debug\n" + all}, challenge.Accepted, 3, 3},
		{"a wrong answer", challenge.Run{Output: line(`{"i":0,"got":[0,1]}`) + line(`{"i":1,"got":[0,0]}`) +
			line(`{"i":2,"got":[0,1]}`)}, challenge.WrongAnswer, 2, 3},
		{"an exception", challenge.Run{Output: line(`{"i":0,"error":"IndexError: list index out of range"}`)},
			challenge.RuntimeError, 0, 3},
		{"a syntax error", challenge.Run{Exit: 1, Output: line(`{"compile":"SyntaxError: invalid syntax"}`)},
			challenge.CompileError, 0, 0},
		{"a compiler's errors", challenge.Run{Exit: 1, Output: "./solution.go:3:1: syntax error"},
			challenge.CompileError, 0, 0},
		{"out of time", challenge.Run{Exit: 124, TimedOut: true, Output: line(`{"i":0,"got":[0,1]}`)},
			challenge.TimeLimitExceeded, 1, 3},
		{"out of memory", challenge.Run{Exit: 137, OOM: true}, challenge.MemoryExceeded, 0, 3},
		{"OpsForge failed", challenge.Run{Failure: "the lab is busy"}, challenge.Error, 0, 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			res := challenge.Judge(d, d.Cases, c.run)
			if res.Verdict != c.verdict || res.Passed != c.passed || len(res.Cases) != c.cases || res.Total != 3 {
				t.Fatalf("%+v", res)
			}
		})
	}
	res := challenge.Judge(d, d.Cases, challenge.Run{Output: "debug\n" + all})
	if res.Output != "debug" || string(res.Cases[0].Got) != "[1,0]" || *res.Cases[0].MS != 0.1 {
		t.Fatalf("the program's own output is kept apart: %+v", res)
	}
	res = challenge.Judge(d, d.Cases, challenge.Run{Exit: 1, Output: "./solution.go:3:1: syntax error"})
	if res.Output != "./solution.go:3:1: syntax error" {
		t.Fatalf("%+v", res)
	}
}

func TestASubmissionShowsOnlyItsFirstFailingCase(t *testing.T) {
	d := twoSum()
	res := challenge.Judge(d, d.Cases, challenge.Run{StopAtFirstFail: true,
		Output: line(`{"i":0,"got":[0,1]}`) + line(`{"i":1,"got":[9,9]}`) + line(`{"i":2,"error":"boom"}`)})
	if res.Verdict != challenge.WrongAnswer || res.Passed != 1 || len(res.Cases) != 1 || res.Cases[0].N != 2 {
		t.Fatalf("%+v", res)
	}
	res = challenge.Judge(d, d.Cases, challenge.Run{StopAtFirstFail: true, Output: line(`{"i":0,"got":[0,1]}`) +
		line(`{"i":1,"got":[1,2]}`) + line(`{"i":2,"got":[1,0]}`)})
	if res.Verdict != challenge.Accepted || len(res.Cases) != 0 {
		t.Fatalf("%+v", res)
	}
}

// The judges, run here with the host's interpreters (each skipped when missing), against right and wrong solutions:
// what OpsForge's program step runs in its pod.
var solutions = map[string]struct{ right, wrong, broken string }{
	"python": {
		right: "def two_sum(nums, target):\n    seen = {}\n    for i, n in enumerate(nums):\n" +
			"        if target - n in seen:\n            return [seen[target - n], i]\n        seen[n] = i\n",
		wrong:  "def two_sum(nums, target):\n    print('trying', target)\n    return [0, 0]\n",
		broken: "def two_sum(nums, target)\n    return []\n",
	},
	"javascript": {
		right: "function twoSum(nums, target) {\n  const seen = new Map();\n  for (let i = 0; i < nums.length; i++) {\n" +
			"    if (seen.has(target - nums[i])) return [seen.get(target - nums[i]), i];\n" +
			"    seen.set(nums[i], i);\n  }\n}\n",
		wrong:  "function twoSum(nums, target) {\n  console.log('trying', target);\n  return [0, 0];\n}\n",
		broken: "function twoSum(nums, target) {\n  return [\n}\n",
	},
	"go": {
		right: "package main\n\nfunc TwoSum(nums []int, target int) []int {\n\tseen := map[int]int{}\n" +
			"\tfor i, n := range nums {\n\t\tif j, ok := seen[target-n]; ok {\n\t\t\treturn []int{j, i}\n\t\t}\n" +
			"\t\tseen[n] = i\n\t}\n\treturn nil\n}\n",
		wrong:  "package main\n\nimport \"fmt\"\n\nfunc TwoSum(nums []int, target int) []int {\n\tfmt.Println(\"trying\", target)\n\treturn []int{0, 0}\n}\n",
		broken: "package main\n\nfunc TwoSum(nums []int, target int) []int {\n\treturn nums[\n}\n",
	},
}

func judgeLocally(t *testing.T, d challenge.Def, lang, code string) challenge.Result {
	t.Helper()
	l := challenge.Languages[lang]
	if _, err := exec.LookPath(l.Cmd[0]); err != nil {
		t.Skipf("%s is not installed", l.Cmd[0])
	}
	files, err := challenge.Files(d, l, code, d.Cases)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	for name, text := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	cmd := exec.Command(l.Cmd[0], l.Cmd[1:]...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOFLAGS=", "GO111MODULE=", "GOWORK=off", "PYTHONDONTWRITEBYTECODE=1")
	out, err := cmd.CombinedOutput()
	exit := 0
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		exit = ee.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	return challenge.Judge(d, d.Cases, challenge.Run{Exit: float64(exit), Output: string(out)})
}

func TestTheJudges(t *testing.T) {
	for lang, s := range solutions {
		t.Run(lang, func(t *testing.T) {
			d := twoSum()
			if res := judgeLocally(t, d, lang, s.right); res.Verdict != challenge.Accepted || res.Passed != 3 {
				t.Fatalf("right: %+v", res)
			}
			res := judgeLocally(t, d, lang, s.wrong)
			if res.Verdict != challenge.WrongAnswer || res.Passed != 0 || !strings.Contains(res.Output, "trying 9") ||
				!challenge.Same(res.Cases[0].Got, raw("[0,0]"), "exact") {
				t.Fatalf("wrong: %+v", res)
			}
			if res := judgeLocally(t, d, lang, s.broken); res.Verdict != challenge.CompileError || res.Output == "" {
				t.Fatalf("broken: %+v", res)
			}
		})
	}
}

func TestTheJudgesReportExceptionsPerCase(t *testing.T) {
	d := twoSum()
	for lang, code := range map[string]string{
		"python":     "def two_sum(nums, target):\n    return [nums[5], 0]\n",
		"javascript": "function twoSum(nums, target) { if (nums.length < 4) throw new RangeError('short'); return [0, 1]; }",
		"go":         "package main\n\nfunc TwoSum(nums []int, target int) []int { return []int{nums[5], 0} }\n",
	} {
		t.Run(lang, func(t *testing.T) {
			res := judgeLocally(t, d, lang, code)
			if res.Verdict != challenge.RuntimeError || res.Cases[1].Error == "" {
				t.Fatalf("%+v", res)
			}
		})
	}
}
