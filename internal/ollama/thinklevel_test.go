package ollama

import (
	"encoding/json"
	"testing"
)

func TestThinkLevelUnmarshal(t *testing.T) {
	cases := []struct {
		in       string
		want     ThinkLevel
		wantErr  bool
	}{
		{in: `"low"`, want: "low"},
		{in: `"medium"`, want: "medium"},
		{in: `"high"`, want: "high"},
		{in: `"max"`, want: "max"},
		{in: `"off"`, want: "off"},
		{in: `"auto"`, want: "auto"},
		{in: `" MEDIUM "`, want: "medium"},
		{in: `true`, want: "auto"},
		{in: `false`, want: "off"},
		{in: `123`, wantErr: true},
	}
	for _, c := range cases {
		var v ThinkLevel
		err := json.Unmarshal([]byte(c.in), &v)
		if c.wantErr {
			if err == nil {
				t.Errorf("Unmarshal(%s): expected error, got %q", c.in, v)
			}
			continue
		}
		if err != nil {
			t.Errorf("Unmarshal(%s): unexpected error: %v", c.in, err)
			continue
		}
		if v != c.want {
			t.Errorf("Unmarshal(%s) = %q, want %q", c.in, v, c.want)
		}
	}
}

func TestThinkLevelMarshal(t *testing.T) {
	cases := []struct {
		v    ThinkLevel
		want string
	}{
		{v: "off", want: "false"},
		{v: "auto", want: "true"},
		{v: "on", want: "true"},
		{v: "low", want: `"low"`},
		{v: "medium", want: `"medium"`},
		{v: "high", want: `"high"`},
		{v: "max", want: `"max"`},
	}
	for _, c := range cases {
		got, err := json.Marshal(c.v)
		if err != nil {
			t.Errorf("Marshal(%q): unexpected error: %v", c.v, err)
			continue
		}
		if string(got) != c.want {
			t.Errorf("Marshal(%q) = %s, want %s", c.v, got, c.want)
		}
	}
}
