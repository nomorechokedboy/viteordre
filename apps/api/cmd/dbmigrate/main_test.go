package main

import (
	"bytes"
	"io"
	"reflect"
	"strings"
	"testing"
)

func TestParseArgs(t *testing.T) {
	tests := []struct {
		name string
		args []string
		pos  []string
		chk  func(*options) bool
	}{
		{"flags before and after", []string{"--data-dir", "d", "up", "--yes"}, []string{"up"},
			func(o *options) bool { return o.dataDir == "d" && o.yes }},
		{"negative number is positional", []string{"force", "-1"}, []string{"force", "-1"}, nil},
		{"double dash", []string{"force", "--", "-1"}, []string{"force", "-1"}, nil},
		{"flag between positionals", []string{"down", "--merchant", "a,b", "2"}, []string{"down", "2"},
			func(o *options) bool { return o.merchants == "a,b" }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			o, pos, err := parseArgs(tt.args, io.Discard)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(pos, tt.pos) {
				t.Fatalf("positionals = %v, want %v", pos, tt.pos)
			}
			if tt.chk != nil && !tt.chk(o) {
				t.Fatalf("options not as expected: %+v", o)
			}
		})
	}
}

func TestParseCommand(t *testing.T) {
	ok := []struct {
		name string
		args []string
		want command
	}{
		{"up", nil, command{name: "up", all: true}},
		{"up", []string{"2"}, command{name: "up", steps: 2}},
		{"down", []string{"1"}, command{name: "down", steps: 1}},
		{"down", []string{"all"}, command{name: "down", all: true}},
		{"goto", []string{"3"}, command{name: "goto", ver: 3}},
		{"force", []string{"-1"}, command{name: "force", ver: -1}},
		{"force", []string{"4"}, command{name: "force", ver: 4}},
		{"version", nil, command{name: "version"}},
	}
	for _, tt := range ok {
		got, err := parseCommand(tt.name, tt.args)
		if err != nil || got != tt.want {
			t.Errorf("%s %v = %+v, %v; want %+v", tt.name, tt.args, got, err, tt.want)
		}
	}
	bad := []struct {
		name string
		args []string
	}{
		{"up", []string{"0"}}, {"up", []string{"x"}}, {"up", []string{"1", "2"}},
		{"down", nil}, {"down", []string{"0"}}, {"down", []string{"-2"}},
		{"goto", []string{"0"}}, {"force", []string{"-2"}}, {"force", nil},
		{"version", []string{"1"}}, {"drop", nil}, {"nope", nil},
	}
	for _, tt := range bad {
		if _, err := parseCommand(tt.name, tt.args); err == nil {
			t.Errorf("%s %v: expected an error", tt.name, tt.args)
		}
	}
}

func TestRunRejectsBadInput(t *testing.T) {
	for _, args := range [][]string{
		nil,
		{"control"},
		{"nope", "up"},
		{"control", "drop"},
		{"control", "down"},
		{"control", "--merchant", "x", "version"},
		{"tenant", "up"}, // no --db, --merchant or --all-tenants
	} {
		var out, errw bytes.Buffer
		if code := run(args, &out, &errw); code == 0 {
			t.Errorf("run(%v) = 0, want a failure code (stdout %q stderr %q)", args, out.String(), errw.String())
		}
	}
}

func TestRunCreateWritesFiles(t *testing.T) {
	dir := t.TempDir()
	var out, errw bytes.Buffer
	if code := run([]string{"tenant", "--source", dir, "create", "Add loyalty points"}, &out, &errw); code != 0 {
		t.Fatalf("exit %d: %s", code, errw.String())
	}
	if !strings.Contains(out.String(), "000001_add_loyalty_points.up.sql") {
		t.Fatalf("unexpected output %q", out.String())
	}
}
