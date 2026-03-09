package config

import (
	"os"
	"testing"
)

func writeTempFile(t *testing.T, content string) string {
	t.Helper()
	f, err := os.CreateTemp("", "sc_inv_*.ini")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Remove(f.Name()) })
	if _, err := f.WriteString(content); err != nil {
		t.Fatal(err)
	}
	f.Close()
	return f.Name()
}

func TestIsIPAddress(t *testing.T) {
	cases := []struct {
		s    string
		want bool
	}{
		{"192.168.1.1", true},
		{"127.0.0.1", true},
		{"10.10.44.55", true},
		{"localhost", false},
		{"google.ie", false},
		{"myhost", false},
	}
	for _, c := range cases {
		if got := isIPAddress(c.s); got != c.want {
			t.Errorf("isIPAddress(%q) = %v, want %v", c.s, got, c.want)
		}
	}
}

func TestExtractAddress(t *testing.T) {
	cases := []struct {
		line string
		want string
	}{
		{"myhost ansible_host=10.0.0.5", "10.0.0.5"},
		{"abc 127.0.0.1 abc", "127.0.0.1"},
		{"192.168.1.10", "192.168.1.10"},
		{"google.ie", "google.ie"},
		{"", ""},
		{"key=value foo=bar", ""},
	}
	for _, c := range cases {
		if got := extractAddress(c.line); got != c.want {
			t.Errorf("extractAddress(%q) = %q, want %q", c.line, got, c.want)
		}
	}
}

func TestParseInventory_SimpleGroup(t *testing.T) {
	f := writeTempFile(t, "[servers]\n192.168.1.1\n192.168.1.2\n")
	hosts, err := ParseInventory(f)
	if err != nil {
		t.Fatal(err)
	}
	if len(hosts) != 2 {
		t.Fatalf("want 2 hosts, got %d", len(hosts))
	}
	if hosts[0].Address != "192.168.1.1" || hosts[0].Group != "servers" {
		t.Errorf("unexpected host[0]: %+v", hosts[0])
	}
	if hosts[1].Address != "192.168.1.2" || hosts[1].Group != "servers" {
		t.Errorf("unexpected host[1]: %+v", hosts[1])
	}
}

func TestParseInventory_MultipleGroups(t *testing.T) {
	f := writeTempFile(t, "[web]\n10.0.0.1\n[db]\n10.0.0.2\n")
	hosts, err := ParseInventory(f)
	if err != nil {
		t.Fatal(err)
	}
	if hosts[0].Group != "web" || hosts[1].Group != "db" {
		t.Errorf("unexpected groups: %v %v", hosts[0].Group, hosts[1].Group)
	}
}

func TestParseInventory_SkipsMetaGroups(t *testing.T) {
	f := writeTempFile(t, "[web]\n10.0.0.1\n[all:children]\nweb\n[web:vars]\nfoo=bar\n")
	hosts, err := ParseInventory(f)
	if err != nil {
		t.Fatal(err)
	}
	if len(hosts) != 1 || hosts[0].Address != "10.0.0.1" {
		t.Errorf("unexpected hosts: %v", hosts)
	}
}

func TestParseInventory_IgnoresCommentsAndBlanks(t *testing.T) {
	f := writeTempFile(t, "# top comment\n\n[group]\n; inline\n\n10.0.0.1\n\n# another\n10.0.0.2\n")
	hosts, err := ParseInventory(f)
	if err != nil {
		t.Fatal(err)
	}
	if len(hosts) != 2 {
		t.Errorf("want 2 hosts, got %d", len(hosts))
	}
}

func TestParseInventory_AnsibleHostKey(t *testing.T) {
	f := writeTempFile(t, "[prod]\nmyalias ansible_host=172.16.0.1\n")
	hosts, err := ParseInventory(f)
	if err != nil {
		t.Fatal(err)
	}
	if len(hosts) != 1 || hosts[0].Address != "172.16.0.1" {
		t.Errorf("unexpected hosts: %v", hosts)
	}
}

func TestParseInventory_HostnameEntry(t *testing.T) {
	f := writeTempFile(t, "[hosts]\ngoogle.ie\n")
	hosts, err := ParseInventory(f)
	if err != nil {
		t.Fatal(err)
	}
	if len(hosts) != 1 || hosts[0].Address != "google.ie" {
		t.Errorf("unexpected hosts: %v", hosts)
	}
}

func TestParseInventory_EmptyFileFails(t *testing.T) {
	f := writeTempFile(t, "# just a comment\n")
	_, err := ParseInventory(f)
	if err == nil {
		t.Error("expected error for empty inventory")
	}
}

func TestParseInventory_MissingFileFails(t *testing.T) {
	_, err := ParseInventory("/tmp/nonexistent_sc_test_xyz.ini")
	if err == nil {
		t.Error("expected error for missing file")
	}
}
