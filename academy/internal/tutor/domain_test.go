package tutor

import (
	"strings"
	"testing"
)

func TestPermitLetsOnlyAcademysToolsThrough(t *testing.T) {
	for title, want := range map[string]bool{
		"mcp__academy__lab_attempt":            true,
		"lab_state (academy MCP Server)":       true,
		"Terminal_Output":                      true,
		"Bash":                                 false,
		"`opsforge kubectl delete ns default`": false,
		"Edit hello.yaml":                      false,
		"":                                     false,
	} {
		if got := Permit(title); got != want {
			t.Errorf("Permit(%q) = %v, want %v", title, got, want)
		}
	}
}

func TestPlainTextRemovesWhatATerminalDraws(t *testing.T) {
	out := "\x1b]0;learner@lab: ~\x07\x1b[01;32mlearner\x1b[00m$ opsforge kubectl get pods\r\n" +
		"NAME    READY\r\nhello   0/1\r\n" +
		"progress 10%\rprogress 100%\r\n" +
		"bell\x07 and \x1b(B tab\tkept\r\n"
	want := "learner$ opsforge kubectl get pods\nNAME    READY\nhello   0/1\nprogress 100%\nbell and  tab\tkept\n"
	if got := PlainText([]byte(out)); got != want {
		t.Fatalf("got %q\nwant %q", got, want)
	}
	if got := PlainText([]byte("broken \xff utf-8")); got != "broken  utf-8" {
		t.Fatalf("%q", got)
	}
}

func TestTailStartsOnALine(t *testing.T) {
	out := []byte("first line\nsecond line\nthird\n")
	if got := string(Tail(out, 100)); got != string(out) {
		t.Fatalf("%q", got)
	}
	if got := string(Tail(out, 15)); got != "third\n" {
		t.Fatalf("%q", got)
	}
	if got := string(Tail([]byte("no newline at all"), 4)); got != " all" {
		t.Fatalf("%q", got)
	}
}

func TestPreambleNamesTheLabAndTheTools(t *testing.T) {
	p := Preamble(Attempt{Title: "Regions and latency"})
	for _, want := range append([]string{`"Regions and latency"`, "opsforge kubectl"}, Tools...) {
		if !strings.Contains(p, want) {
			t.Errorf("the preamble does not mention %q", want)
		}
	}
}
