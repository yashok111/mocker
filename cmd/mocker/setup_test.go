package main

import (
	"regexp"
	"slices"
	"strings"
	"testing"
)

const exampleEnv = "# comment\nMOCKER_BASE_DOMAIN=mock.local\nMOCKER_ADMIN_HOST=mocker.local\nMOCKER_ROUTING=host\nMOCKER_SHARED_PASSWORD_HASH=REPLACE_ME\nMOCKER_MCP_KEY=\n"

func TestSetup_envValueLastAssignmentWins(t *testing.T) {
	t.Parallel()
	env := []byte("A=1\nB=x=y\nA=2\n")
	if got := envValue(env, "A"); got != "2" {
		t.Errorf("A = %q, want 2 (last wins)", got)
	}
	if got := envValue(env, "B"); got != "x=y" {
		t.Errorf("B = %q, want everything after the first '='", got)
	}
	if got := envValue(env, "C"); got != "" {
		t.Errorf("C = %q, want empty", got)
	}
}

func TestSetup_renderEnvWritesInPlaceAndFixesPathRouting(t *testing.T) {
	t.Parallel()
	out := renderEnv([]byte(exampleEnv), "$argon2id$v=19$m=1,t=1,p=1$c$h", "dev.local", "abc")
	s := string(out)
	for _, want := range []string{
		"MOCKER_SHARED_PASSWORD_HASH=$argon2id$v=19$m=1,t=1,p=1$c$h\n",
		"MOCKER_ROUTING=path\n",
		"MOCKER_ADMIN_HOST=dev.local\n",
		"MOCKER_MCP_KEY=abc\n",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("rendered .env lacks %q:\n%s", want, s)
		}
	}
	if strings.Contains(s, "REPLACE_ME") || strings.Count(s, "MOCKER_ROUTING=") != 1 {
		t.Errorf("values must be rewritten in place, not appended:\n%s", s)
	}
	if !strings.HasPrefix(s, "# comment\n") {
		t.Errorf("comments must survive:\n%s", s)
	}
	// No MCP key: the line stays as the example had it.
	if s2 := string(renderEnv([]byte(exampleEnv), "h", "a.b", "")); !strings.Contains(s2, "MOCKER_MCP_KEY=\n") {
		t.Errorf("empty mcp key must leave the example's line: %s", s2)
	}
	// A key absent from the example is appended.
	if s3 := string(setEnvValue([]byte("A=1\n"), "B", "2")); s3 != "A=1\nB=2\n" {
		t.Errorf("append = %q", s3)
	}
}

func TestSetup_composeEnvMirrorsTheScript(t *testing.T) {
	t.Parallel()
	env, err := composeEnv([]byte(exampleEnv), "10.9.8.0/24", 9443)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"MOCKER_BASE_DOMAIN=mock.local", "MOCKER_ADMIN_HOST=mocker.local",
		"MOCKER_TLS_SUBNET=10.9.8.0/24", "MOCKER_TLS_GATEWAY=10.9.8.1", "MOCKER_TLS_CADDY_IP=10.9.8.254",
		"MOCKER_TLS_PORT=9443",
	} {
		if !slices.Contains(env, want) {
			t.Errorf("compose env lacks %q: %v", want, env)
		}
	}
	if !slices.ContainsFunc(env, func(s string) bool { return strings.HasPrefix(s, "COMPOSE_ENV_FILES=") }) {
		t.Errorf("COMPOSE_ENV_FILES must point compose away from .env: %v", env)
	}
	for name, bad := range map[string]struct {
		env    string
		subnet string
		port   int
	}{
		"missing admin host": {"MOCKER_BASE_DOMAIN=mock.local\n", "10.9.8.0/24", 8443},
		"host with a port":   {"MOCKER_BASE_DOMAIN=mock.local\nMOCKER_ADMIN_HOST=mocker.local:8443\n", "10.9.8.0/24", 8443},
		"host with a brace":  {"MOCKER_BASE_DOMAIN=mock.local\nMOCKER_ADMIN_HOST={bad}\n", "10.9.8.0/24", 8443},
		"subnet not a /24":   {exampleEnv, "10.9.0.0/16", 8443},
		"subnet not .0":      {exampleEnv, "10.9.8.5/24", 8443},
		"port out of range":  {exampleEnv, "10.9.8.0/24", 70000},
	} {
		if _, err := composeEnv([]byte(bad.env), bad.subnet, bad.port); err == nil {
			t.Errorf("%s: accepted, want a refusal", name)
		}
	}
	if got := composeArgs("up", "-d"); strings.Join(got, " ") != "compose -f docker-compose.yml -f docker-compose.tls.yml up -d" {
		t.Errorf("composeArgs = %v", got)
	}
}

func TestSetup_composeVersionOK(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]bool{"2.30.0": true, "v2.31.1": true, "3.0.0": true, "2.29.9": false, "2.24.0": false, "1.29.2": false} {
		ok, err := composeVersionOK(in)
		if err != nil || ok != want {
			t.Errorf("composeVersionOK(%q) = %v, %v; want %v", in, ok, err, want)
		}
	}
	if _, err := composeVersionOK("garbage"); err == nil {
		t.Error("garbage accepted")
	}
}

func TestSetup_secretsHaveTheDocumentedShape(t *testing.T) {
	t.Parallel()
	pw, err := generatePassword()
	if err != nil || !regexp.MustCompile(`^[A-Za-z0-9]{20,24}$`).MatchString(pw) {
		t.Errorf("password = %q, %v; want 20..24 base64 chars without / + =", pw, err)
	}
	key, err := generateMCPKey()
	if err != nil || !regexp.MustCompile(`^[0-9a-f]{48}$`).MatchString(key) {
		t.Errorf("mcp key = %q, %v; want 48 hex chars (>= the 32-byte floor)", key, err)
	}
}

func TestSetup_trustPlanPerOS(t *testing.T) {
	t.Parallel()
	debian := func(p string) bool { return p == "/usr/local/share/ca-certificates" }
	rhel := func(p string) bool { return p == "/etc/pki/ca-trust/source/anchors" }
	none := func(string) bool { return false }

	cmds, manual := trustPlan("linux", "/x/root.crt", debian)
	if len(cmds) != 2 || cmds[1][1] != "update-ca-certificates" || !strings.Contains(manual, "update-ca-certificates") {
		t.Errorf("debian plan = %v / %q", cmds, manual)
	}
	cmds, _ = trustPlan("linux", "/x/root.crt", rhel)
	if len(cmds) != 2 || cmds[1][1] != "update-ca-trust" {
		t.Errorf("rhel plan = %v", cmds)
	}
	if cmds, manual := trustPlan("linux", "/x/root.crt", none); len(cmds) != 0 || manual == "" {
		t.Errorf("unknown distro must plan nothing and explain: %v / %q", cmds, manual)
	}
	if cmds, _ := trustPlan("darwin", "/x/root.crt", none); len(cmds) != 1 || cmds[0][1] != "security" || cmds[0][0] != "sudo" {
		t.Errorf("darwin plan = %v", cmds)
	}
	if cmds, _ := trustPlan("windows", `C:\x\root.crt`, none); len(cmds) != 1 || cmds[0][0] != "certutil" || !slices.Contains(cmds[0], "Root") {
		t.Errorf("windows plan = %v", cmds)
	}
	if hostsCommand("windows", "mocker.local") != nil {
		t.Error("windows appends directly, no command")
	}
	if c := hostsCommand("linux", "mocker.local"); len(c) != 1 || c[0][0] != "sudo" || !strings.Contains(c[0][3], "127.0.0.1 mocker.local") {
		t.Errorf("linux hosts command = %v", c)
	}
	if hostsPath("linux") != "/etc/hosts" || !strings.HasSuffix(hostsPath("windows"), `drivers\etc\hosts`) {
		t.Errorf("hosts paths: %q %q", hostsPath("linux"), hostsPath("windows"))
	}
}

func TestSetup_refusesOutsideACheckout(t *testing.T) {
	t.Parallel()
	err := runSetup([]string{"up", "-dir", t.TempDir(), "-yes"}, strings.NewReader(""), &strings.Builder{}, &strings.Builder{})
	if err == nil || !strings.Contains(err.Error(), "not a mocker checkout") {
		t.Fatalf("err = %v, want the checkout refusal before docker is ever touched", err)
	}
	if err := runSetup([]string{"bogus"}, strings.NewReader(""), &strings.Builder{}, &strings.Builder{}); err == nil {
		t.Fatal("unknown verb accepted")
	}
}
