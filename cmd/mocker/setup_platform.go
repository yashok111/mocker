package main

import (
	"os"
)

// The per-OS half of `mocker setup`: where the hosts file is, and which
// commands put Caddy's local root into the system trust store. Each
// function answers a PLAN (argv lists plus the manual instruction), never
// runs anything, so the plan for every OS is testable on this one.

// rootCertName is the file the wizard writes next to .env and the name
// under which the root lands in a trust store. Its bytes are the stable
// root from .tls-ca (ensureLocalCA) — the wizard no longer copies anything
// out of a container.
const rootCertName = "mocker-root.crt"

// hostsPath is the hosts file for goos.
func hostsPath(goos string) string {
	if goos == "windows" {
		// A literal backslash join, not filepath.Join: this function
		// answers for Windows when asked on any OS (the test does), and
		// SystemRoot is C:\Windows on a real one.
		root := os.Getenv("SystemRoot")
		if root == "" {
			root = `C:\Windows`
		}
		return root + `\System32\drivers\etc\hosts`
	}
	return "/etc/hosts"
}

// hostsCommand is the privileged append of `127.0.0.1 host` for a Unix
// goos — sudo runs the shell, and host was validated as a bare host name
// before it is ever interpolated. On Windows the wizard appends directly
// (an elevated terminal has the right; a plain one gets the manual line),
// so there is no command to plan.
func hostsCommand(goos, host string) [][]string {
	if goos == "windows" {
		return nil
	}
	return [][]string{{"sudo", "sh", "-c", "printf '\\n127.0.0.1 " + host + "\\n' >> /etc/hosts"}}
}

// trustPlan is the commands that install certPath as a trusted root for
// goos, and the sentence a human runs when they fail (no sudo, no admin
// terminal, an unknown distribution). Linux is decided by which anchor
// directory exists — Debian/Ubuntu's or Fedora/RHEL's — through exists,
// injected so the test can pick either.
func trustPlan(goos, certPath string, exists func(string) bool) (cmds [][]string, manual string) {
	switch goos {
	case "darwin":
		return [][]string{{"sudo", "security", "add-trusted-cert", "-d", "-r", "trustRoot", "-k", "/Library/Keychains/System.keychain", certPath}},
			"sudo security add-trusted-cert -d -r trustRoot -k /Library/Keychains/System.keychain " + certPath
	case "windows":
		return [][]string{{"certutil", "-addstore", "-f", "Root", certPath}},
			"in an Administrator terminal: certutil -addstore -f Root " + certPath +
				" (or double-click the file → Install → Local Machine → Trusted Root Certification Authorities)"
	default:
		switch {
		case exists("/usr/local/share/ca-certificates"):
			dst := "/usr/local/share/ca-certificates/" + rootCertName
			return [][]string{{"sudo", "cp", certPath, dst}, {"sudo", "update-ca-certificates"}},
				"sudo cp " + certPath + " " + dst + " && sudo update-ca-certificates"
		case exists("/etc/pki/ca-trust/source/anchors"):
			dst := "/etc/pki/ca-trust/source/anchors/" + rootCertName
			return [][]string{{"sudo", "cp", certPath, dst}, {"sudo", "update-ca-trust"}},
				"sudo cp " + certPath + " " + dst + " && sudo update-ca-trust"
		default:
			return nil, "add " + certPath + " to your distribution's trust store (no known anchor directory found)"
		}
	}
}

// firefoxNote is the one browser that reads neither the Linux system store
// nor, by default, the macOS/Windows one.
const firefoxNote = "Firefox keeps its own store: Settings → Privacy & Security → Certificates → View → Authorities → Import " +
	rootCertName + " (or set security.enterprise_roots.enabled=true in about:config)."
