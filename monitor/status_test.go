package monitor

import "testing"

func TestParseStatus(t *testing.T) {
	cases := []struct {
		input   string
		want    ServiceStatus
		wantMsg string
	}{
		{"active", StatusActive, ""},
		{"  active  ", StatusActive, ""},
		{"inactive", StatusInactive, ""},
		{"failed", StatusFailed, ""},
		{"not-found", StatusNotFound, ""},
		{"not found", StatusNotFound, ""},
		{"Unit nginx.service could not be found", StatusNotFound, ""},
		{"", StatusUnknown, ""},
		{"   ", StatusUnknown, ""},
		{"activating", StatusError, "activating"},
	}
	for _, c := range cases {
		st, msg := ParseStatus(c.input)
		if st != c.want {
			t.Errorf("ParseStatus(%q) status = %v, want %v", c.input, st, c.want)
		}
		if msg != c.wantMsg {
			t.Errorf("ParseStatus(%q) msg = %q, want %q", c.input, msg, c.wantMsg)
		}
	}
}

func TestDisplay(t *testing.T) {
	cases := []struct {
		st  ServiceStatus
		msg string
		out string
	}{
		{StatusUnknown, "", "???"},
		{StatusActive, "", "active"},
		{StatusInactive, "", "inactive"},
		{StatusFailed, "", "FAILED"},
		{StatusNotFound, "", "not found"},
		{StatusError, "some error", "some error"},
	}
	for _, c := range cases {
		if got := c.st.Display(c.msg); got != c.out {
			t.Errorf("Display(%v, %q) = %q, want %q", c.st, c.msg, got, c.out)
		}
	}
}

func TestParseSystemctlOutputLines(t *testing.T) {
	output := "active\ninactive\nfailed\n"
	lines := []string{"active", "inactive", "failed"}
	want := []ServiceStatus{StatusActive, StatusInactive, StatusFailed}
	for i, line := range lines {
		st, _ := ParseStatus(line)
		if st != want[i] {
			t.Errorf("line %q: got %v, want %v", output, st, want[i])
		}
	}
}

func TestClassifySSHError(t *testing.T) {
	cases := []struct {
		err  string
		want string
	}{
		{"connection timed out", "connection request timed out"},
		{"timeout exceeded", "connection request timed out"},
		{"Permission denied (publickey)", "authentication error"},
		{"authentication failed", "authentication error"},
		{"connection refused", "connection error"},
		{"no route to host", "connection error"},
	}
	for _, c := range cases {
		if got := ClassifySSHError(c.err); got != c.want {
			t.Errorf("ClassifySSHError(%q) = %q, want %q", c.err, got, c.want)
		}
	}
}
