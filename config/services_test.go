package config

import (
	"os"
	"testing"
)

func writeTempYAML(t *testing.T, content string) string {
	t.Helper()
	f, err := os.CreateTemp("", "sc_svc_*.yaml")
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

func TestParseServices_SingleEntry(t *testing.T) {
	f := writeTempYAML(t, "services:\n  nginx:\n    commands:\n      - nginx -T\n")
	configs, err := ParseServices(f)
	if err != nil {
		t.Fatal(err)
	}
	if len(configs) != 1 {
		t.Fatalf("want 1 config, got %d", len(configs))
	}
	c := configs[0]
	if c.NamePattern != "nginx" {
		t.Errorf("unexpected name: %q", c.NamePattern)
	}
	if len(c.Commands) != 1 || c.Commands[0] != "nginx -T" {
		t.Errorf("unexpected commands: %v", c.Commands)
	}
	if c.IsGlob {
		t.Error("nginx should not be a glob")
	}
}

func TestParseServices_GlobStar(t *testing.T) {
	f := writeTempYAML(t, "services:\n  nginx*:\n    commands:\n      - nginx -T\n")
	configs, err := ParseServices(f)
	if err != nil {
		t.Fatal(err)
	}
	if !configs[0].IsGlob {
		t.Error("nginx* should be a glob")
	}
}

func TestParseServices_GlobQuestionMark(t *testing.T) {
	f := writeTempYAML(t, "services:\n  nginx?:\n    commands: []\n")
	configs, err := ParseServices(f)
	if err != nil {
		t.Fatal(err)
	}
	if !configs[0].IsGlob {
		t.Error("nginx? should be a glob")
	}
}

func TestParseServices_GlobBracket(t *testing.T) {
	f := writeTempYAML(t, "services:\n  \"nginx[0-9]\":\n    commands: []\n")
	configs, err := ParseServices(f)
	if err != nil {
		t.Fatal(err)
	}
	if !configs[0].IsGlob {
		t.Error("nginx[0-9] should be a glob")
	}
}

func TestParseServices_FilesAndCommands(t *testing.T) {
	f := writeTempYAML(t, "services:\n  nginx:\n    files:\n      - /etc/nginx/nginx.conf\n    commands:\n      - nginx -T\n")
	configs, err := ParseServices(f)
	if err != nil {
		t.Fatal(err)
	}
	c := configs[0]
	if len(c.Files) != 1 || c.Files[0] != "/etc/nginx/nginx.conf" {
		t.Errorf("unexpected files: %v", c.Files)
	}
	if len(c.Commands) != 1 || c.Commands[0] != "nginx -T" {
		t.Errorf("unexpected commands: %v", c.Commands)
	}
}

func TestParseServices_SortedByName(t *testing.T) {
	f := writeTempYAML(t, "services:\n  zebra:\n    commands: []\n  alpha:\n    commands: []\n  middle:\n    commands: []\n")
	configs, err := ParseServices(f)
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, len(configs))
	for i, c := range configs {
		names[i] = c.NamePattern
	}
	want := []string{"alpha", "middle", "zebra"}
	for i, n := range want {
		if names[i] != n {
			t.Errorf("names[%d] = %q, want %q", i, names[i], n)
		}
	}
}

func TestParseServices_EmptyDefaults(t *testing.T) {
	f := writeTempYAML(t, "services:\n  bare:\n")
	configs, err := ParseServices(f)
	if err != nil {
		t.Fatal(err)
	}
	if len(configs[0].Files) != 0 || len(configs[0].Commands) != 0 {
		t.Errorf("expected empty slices, got files=%v commands=%v", configs[0].Files, configs[0].Commands)
	}
}

func TestParseServices_MissingFileFails(t *testing.T) {
	_, err := ParseServices("/tmp/nonexistent_sc_test_xyz.yaml")
	if err == nil {
		t.Error("expected error for missing file")
	}
}

func TestParseServices_InvalidYAMLFails(t *testing.T) {
	f := writeTempYAML(t, "this is not: valid: yaml: at: all\n  broken\n")
	_, err := ParseServices(f)
	if err == nil {
		t.Error("expected error for invalid YAML")
	}
}
