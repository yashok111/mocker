package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/yashok111/mocker/internal/auth"
	"github.com/yashok111/mocker/internal/jsonx"
	"github.com/yashok111/mocker/internal/probe"
)

// `mocker setup` is the install wizard for a colleague's own machine: the
// steps README's "HTTPS" section walks through by hand — .env with a real
// hash, the compose overlay with Caddy's local CA, the CA root exported and
// trusted, the admin host in the hosts file, readiness checked — as one
// command that can be re-run. It replaces scripts/init-env.sh and
// scripts/compose-tls.sh for this path (both stay for the Makefile and the
// smoke tests) because it also has to run on macOS and Windows.
//
// What it does NOT do, on purpose: it does not ship an image (the colleague
// has the repository, and `docker compose up --build` builds it there —
// the owner's choice 2026-09-03), it does not pick host routing (one
// machine has no wildcard DNS, so MOCKER_ROUTING=path is fixed and every
// workspace lives under https://<admin host>:<port>/w/<slug>), and it never
// rewrites an existing .env (the file holds the deployment's hash;
// scripts/init-env.sh's rule).
//
// Three verbs: `up` (the default; idempotent), `down` (stop, keep the data
// and CA volumes) and `status`.

type setupOptions struct {
	dir       string
	password  string
	adminHost string
	port      int
	subnet    string
	mcp       bool
	yes       bool
	noTrust   bool
	noHosts   bool
	noBuild   bool
}

// setupTimeouts bound the two waits: the CA root appears once Caddy has
// started (it depends on mocker's healthcheck, which itself waits on the
// image build having finished), and readiness follows.
const (
	rootExportWait = 3 * time.Minute
	readinessWait  = 3 * time.Minute
	pollEvery      = 2 * time.Second
)

func runSetup(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	verb := "up"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		verb, args = args[0], args[1:]
	}
	var o setupOptions
	fs := flag.NewFlagSet("mocker setup", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&o.dir, "dir", ".", "repository checkout to run in (docker-compose.yml, deploy/Caddyfile, .env.example)")
	fs.StringVar(&o.password, "password", os.Getenv("MOCKER_PASSWORD"), "admin password for a NEW .env (default: generated and printed once; also $MOCKER_PASSWORD)")
	fs.StringVar(&o.adminHost, "admin-host", "mocker.local", "admin host name for a NEW .env")
	fs.IntVar(&o.port, "port", defaultTLSPort, "https port on 127.0.0.1")
	fs.StringVar(&o.subnet, "subnet", defaultTLSSubnet, "docker network for the stack, an IPv4 /24 (change on a 'Pool overlaps' error)")
	fs.BoolVar(&o.mcp, "mcp", false, "also mint MOCKER_MCP_KEY for a NEW .env (the agent door)")
	fs.BoolVar(&o.yes, "yes", false, "no questions: defaults for everything not given as a flag")
	fs.BoolVar(&o.noTrust, "no-trust", false, "do not install the CA root into the system trust store (print the command instead)")
	fs.BoolVar(&o.noHosts, "no-hosts", false, "do not touch the hosts file (print the line instead)")
	fs.BoolVar(&o.noBuild, "no-build", false, "start without rebuilding the image")
	fs.Usage = func() {
		_, _ = fmt.Fprintln(stderr, "usage: mocker setup [up|down|status] [flags]")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	// Everything below runs INSIDE the checkout — relative paths for the
	// files the wizard reads and writes, Dir-less exec for docker — so the
	// -dir value itself never reaches a file operation or an argv.
	if err := os.Chdir(o.dir); err != nil {
		return fmt.Errorf("-dir %s: %w", o.dir, err)
	}
	w := &setupRun{o: o, in: bufio.NewReader(stdin), out: stdout, err: stderr, goos: runtime.GOOS, ctx: context.Background()}
	switch verb {
	case "up":
		return w.up()
	case "down":
		return w.down()
	case "status":
		return w.status()
	default:
		fs.Usage()
		return fmt.Errorf("unknown verb %q", verb)
	}
}

type setupRun struct {
	o    setupOptions
	in   *bufio.Reader
	out  io.Writer
	err  io.Writer
	goos string
	ctx  context.Context
	env  []string // compose interpolation environment, from composeEnv
}

// sayLine joins operands the way fmt.Sprintln does, without the newline.
func sayLine(args ...any) string { return strings.TrimSuffix(fmt.Sprintln(args...), "\n") }

// say and warn are the wizard's two voices; a write error to the operator's
// own terminal is not something the wizard can act on, hence the discard.
func (w *setupRun) say(format string, args ...any)  { _, _ = fmt.Fprintf(w.out, format+"\n", args...) }
func (w *setupRun) warn(format string, args ...any) { _, _ = fmt.Fprintf(w.err, format+"\n", args...) }

func (w *setupRun) up() error {
	if err := w.checkDir(); err != nil {
		return err
	}
	if err := w.checkDocker(); err != nil {
		return err
	}
	generated, err := w.ensureEnv()
	if err != nil {
		return err
	}
	envFile, err := os.ReadFile(".env")
	if err != nil {
		return err
	}
	adminHost := envValue(envFile, "MOCKER_ADMIN_HOST")
	if w.env, err = composeEnv(envFile, w.o.subnet, w.o.port); err != nil {
		return err
	}

	w.step("starting the stack (mocker + Caddy with a local CA)")
	upArgs := composeArgs("up", "-d")
	if !w.o.noBuild {
		upArgs = append(upArgs, "--build")
	}
	if err := w.docker(upArgs...); err != nil {
		return fmt.Errorf("docker compose up: %w", err)
	}

	w.step("exporting the CA root")
	rootPEM, err := w.exportRoot()
	if err != nil {
		return err
	}
	// Absolute for the trust commands and the summary: a colleague pastes
	// the manual line from another directory.
	certPath, err := filepath.Abs(rootCertName)
	if err != nil {
		return err
	}

	w.step("waiting for https://" + adminHost + ":" + strconv.Itoa(w.o.port) + "/readyz")
	if err := w.waitReady(adminHost, rootPEM); err != nil {
		return err
	}

	hostsManual := w.ensureHosts(adminHost)
	trustManual := w.ensureTrust(certPath)

	w.summary(adminHost, envFile, generated, certPath, hostsManual, trustManual)
	return nil
}

func (w *setupRun) down() error {
	if err := w.checkDir(); err != nil {
		return err
	}
	envFile, err := os.ReadFile(".env")
	if err != nil {
		return fmt.Errorf("no .env in %s: nothing to stop", w.o.dir)
	}
	if w.env, err = composeEnv(envFile, w.o.subnet, w.o.port); err != nil {
		return err
	}
	return w.docker(composeArgs("down", "--remove-orphans")...)
}

func (w *setupRun) status() error {
	if err := w.checkDir(); err != nil {
		return err
	}
	envFile, err := os.ReadFile(".env")
	if err != nil {
		return fmt.Errorf("no .env in %s: the stack was never set up", w.o.dir)
	}
	if w.env, err = composeEnv(envFile, w.o.subnet, w.o.port); err != nil {
		return err
	}
	if err := w.docker(composeArgs("ps")...); err != nil {
		return err
	}
	rootPEM, err := os.ReadFile(rootCertName)
	if err != nil {
		return fmt.Errorf("no %s next to .env — run `mocker setup` once", rootCertName)
	}
	adminHost := envValue(envFile, "MOCKER_ADMIN_HOST")
	ctx, cancel := context.WithTimeout(context.Background(), probe.Timeout)
	defer cancel()
	out := probe.ReadinessTLS(ctx, w.readyURL(), adminHost, rootPEM)
	if out.Kind != probe.KindResponse || out.Status != http.StatusOK {
		return fmt.Errorf("https://%s:%d is not ready (%s)", adminHost, w.o.port, out.Kind)
	}
	w.say("ready: https://%s:%d", adminHost, w.o.port)
	return nil
}

// --- steps -----------------------------------------------------------------

func (w *setupRun) checkDir() error {
	for _, f := range []string{"docker-compose.yml", "docker-compose.tls.yml", filepath.Join("deploy", "Caddyfile"), ".env.example"} {
		if _, err := os.Stat(f); err != nil {
			return fmt.Errorf("%s is not a mocker checkout: %s is missing (run from the repository root, or pass -dir)", w.o.dir, f)
		}
	}
	return nil
}

func (w *setupRun) checkDocker() error {
	w.step("checking docker")
	if _, err := w.dockerOutput("version", "--format", "{{.Server.Version}}"); err != nil {
		return errors.New("docker is not running or not installed — start Docker Desktop / the docker service and run again")
	}
	short, err := w.dockerOutput("compose", "version", "--short")
	if err != nil {
		return errors.New("docker compose (v2) is not available — install the compose plugin and run again")
	}
	ok, err := composeVersionOK(short)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("docker compose %s is too old: the stack needs %s or newer", strings.TrimSpace(short), minComposeVersion)
	}
	return nil
}

// ensureEnv writes .env from .env.example on the first run and leaves an
// existing one alone. generated is the password it minted, "" when the
// operator supplied one or the file already existed.
func (w *setupRun) ensureEnv() (generated string, err error) {
	const path = ".env"
	if _, err := os.Stat(path); err == nil {
		w.say(sayLine(".env exists — keeping it (password, hosts and routing unchanged; delete it to start over)"))
		return "", nil
	}
	w.step("creating .env")
	adminHost := w.ask("Admin host name", w.o.adminHost)
	if !bareHost.MatchString(adminHost) || strings.Contains(adminHost, ":") {
		return "", fmt.Errorf("admin host %q must be a bare host name (letters, digits, dots, hyphens)", adminHost)
	}
	password := w.o.password
	if password == "" && !w.o.yes {
		password = w.ask("Admin password (empty = generate one)", "")
	}
	if password == "" {
		if password, err = generatePassword(); err != nil {
			return "", err
		}
		generated = password
	}
	mcpKey := ""
	wantMCP := w.o.mcp
	if !w.o.mcp && !w.o.yes {
		wantMCP = strings.HasPrefix(strings.ToLower(w.ask("Enable the MCP door for an agent? (y/N)", "n")), "y")
	}
	if wantMCP {
		if mcpKey, err = generateMCPKey(); err != nil {
			return "", err
		}
	}
	hash, err := auth.HashPassword(password)
	if err != nil {
		return "", err
	}
	example, err := os.ReadFile(".env.example")
	if err != nil {
		return "", err
	}
	// 0600: .env holds the argon2id verifier of the admin password, an
	// offline cracking target for any other local user if world-readable.
	if err := os.WriteFile(path, renderEnv(example, hash, adminHost, mcpKey), 0o600); err != nil { //nolint:gosec // G703: the path is the constant ".env" in the checkout; what is tainted is the CONTENT (the operator's own answers), not where it lands
		return "", err
	}
	return generated, nil
}

// exportRoot copies Caddy's root.crt out of the container, retrying while
// Caddy is still starting (it waits for mocker's healthcheck, which waits
// for the build), and returns the PEM.
func (w *setupRun) exportRoot() ([]byte, error) {
	deadline := time.Now().Add(rootExportWait)
	for {
		if _, err := w.dockerOutput(composeArgs("cp", caddyRootInContainer, rootCertName)...); err == nil {
			return os.ReadFile(rootCertName)
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("caddy did not start within %s — `docker compose -f docker-compose.yml -f docker-compose.tls.yml logs caddy mocker` says why", rootExportWait)
		}
		time.Sleep(pollEvery)
	}
}

func (w *setupRun) readyURL() string {
	return "https://" + net.JoinHostPort("127.0.0.1", strconv.Itoa(w.o.port)) + "/readyz"
}

func (w *setupRun) waitReady(adminHost string, rootPEM []byte) error {
	deadline := time.Now().Add(readinessWait)
	var last probe.Outcome
	for {
		ctx, cancel := context.WithTimeout(context.Background(), probe.Timeout)
		last = probe.ReadinessTLS(ctx, w.readyURL(), adminHost, rootPEM)
		cancel()
		if last.Kind == probe.KindResponse && last.Status == http.StatusOK {
			var body struct {
				OK bool `json:"ok"`
			}
			if jsonx.Unmarshal(last.Body, &body) == nil && body.OK {
				return nil
			}
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("the stack did not become ready within %s (last: %s %d) — `docker compose -f docker-compose.yml -f docker-compose.tls.yml logs` says why", readinessWait, last.Kind, last.Status)
		}
		time.Sleep(pollEvery)
	}
}

// ensureHosts makes adminHost resolve to loopback: nothing when it already
// does, a privileged append otherwise. Returns the manual line when the
// append was skipped or failed.
func (w *setupRun) ensureHosts(adminHost string) (manual string) {
	line := "127.0.0.1 " + adminHost
	if addrs, err := net.DefaultResolver.LookupHost(w.ctx, adminHost); err == nil {
		for _, a := range addrs {
			if ip := net.ParseIP(a); ip != nil && ip.IsLoopback() {
				return ""
			}
		}
	}
	if w.o.noHosts {
		return line
	}
	w.step("adding " + line + " to " + hostsPath(w.goos) + " (asks for your password)")
	if w.goos == "windows" {
		f, err := os.OpenFile(hostsPath(w.goos), os.O_APPEND|os.O_WRONLY, 0)
		if err == nil {
			_, err = f.WriteString("\r\n" + line + "\r\n")
			_ = f.Close()
		}
		if err != nil {
			w.warn(sayLine("could not write the hosts file (not an Administrator terminal?):", err))
			return line
		}
		return ""
	}
	for _, cmd := range hostsCommand(w.goos, adminHost) {
		if err := w.run(cmd[0], cmd[1:]...); err != nil {
			w.warn(sayLine("could not edit /etc/hosts:", err))
			return line
		}
	}
	return ""
}

// ensureTrust installs the exported root; returns the manual command when
// skipped or failed.
func (w *setupRun) ensureTrust(certPath string) (manual string) {
	exists := func(p string) bool { _, err := os.Stat(p); return err == nil }
	cmds, manualCmd := trustPlan(w.goos, certPath, exists)
	if w.o.noTrust || len(cmds) == 0 {
		return manualCmd
	}
	w.step("trusting the CA root system-wide (asks for your password)")
	for _, cmd := range cmds {
		if err := w.run(cmd[0], cmd[1:]...); err != nil {
			w.warn(sayLine("could not install the root:", err))
			return manualCmd
		}
	}
	return ""
}

func (w *setupRun) summary(adminHost string, envFile []byte, generated, certPath, hostsManual, trustManual string) {
	base := fmt.Sprintf("https://%s:%d", adminHost, w.o.port)
	w.say("")
	w.say(sayLine("mocker is up."))
	w.say(sayLine("  panel:      ", base))
	w.say(sayLine("  workspaces: ", base+"/w/<slug>   (the panel shows each workspace's exact URL)"))
	if generated != "" {
		w.say(sayLine("  password:   ", generated, "  (generated, shown ONCE — there is no way to read it back)"))
	}
	if key := envValue(envFile, "MOCKER_MCP_KEY"); key != "" {
		w.say(sayLine("  MCP:        ", base+"/mcp  with header  Authorization: Bearer "+key))
	}
	w.say(sayLine("  CA root:    ", certPath))
	if hostsManual != "" {
		w.say(sayLine("TODO hosts:   add this line to", hostsPath(w.goos)+":", hostsManual))
	}
	if trustManual != "" {
		w.say(sayLine("TODO trust:  ", trustManual))
	}
	w.say(sayLine("  " + firefoxNote))
	w.say(sayLine("  stop: mocker setup down    status: mocker setup status    re-run `mocker setup` any time"))
}

// --- plumbing ---------------------------------------------------------------

func (w *setupRun) step(msg string) { w.say("== %s", msg) }

// ask prompts on stdout and reads one line; with -yes it answers def
// without asking.
func (w *setupRun) ask(label, def string) string {
	if w.o.yes {
		return def
	}
	if def != "" {
		_, _ = fmt.Fprintf(w.out, "%s [%s]: ", label, def)
	} else {
		_, _ = fmt.Fprintf(w.out, "%s: ", label)
	}
	line, _ := w.in.ReadString('\n')
	line = strings.TrimSpace(line)
	if line == "" {
		return def
	}
	return line
}

// docker runs the docker CLI with the compose interpolation environment,
// output streamed to the operator (a build is minutes of it).
func (w *setupRun) docker(args ...string) error {
	return w.run("docker", args...)
}

// dockerOutput is docker with stdout captured and stderr discarded, for the
// checks and the retried cp.
func (w *setupRun) dockerOutput(args ...string) (string, error) {
	cmd := exec.CommandContext(w.ctx, "docker", args...) //nolint:gosec // G204: the wizard's job is to run docker; every argument is a constant or a value validated by composeEnv
	cmd.Env = append(os.Environ(), w.env...)
	out, err := cmd.Output()
	return string(out), err
}

// run executes name with args (the process is already in the checkout), the compose
// environment attached, stdin/stdout/stderr inherited so sudo can prompt
// and a build can stream.
func (w *setupRun) run(name string, args ...string) error {
	cmd := exec.CommandContext(w.ctx, name, args...) //nolint:gosec // G204: docker, sudo, certutil and security with constant argv shapes; the only variable parts (host, paths) are validated above
	cmd.Env = append(os.Environ(), w.env...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = w.out
	cmd.Stderr = w.err
	return cmd.Run()
}
