#!/usr/bin/env bash
# ci-cap.sh — run one command inside a memory- and CPU-capped transient
# systemd scope, so that a runaway step is what dies, not the runner.
#
# The Makefile's own CAP does the same thing on a developer box through the
# USER manager (`systemd-run --user --scope`), and degrades to nothing where
# the user slice has no delegated memory controller — which is the case on a
# hosted CI runner. This wrapper takes the other door: a SYSTEM scope through
# passwordless sudo, then drops straight back to the calling user (`-E` keeps
# the environment setup-go/setup-node prepared; an `ALL` sudoers entry
# implies SETENV, so `-E` is allowed; PATH and HOME are re-applied
# explicitly: sudo's secure_path resets PATH even under `-E`, and the OUTER
# sudo already set HOME=/root, which `-E` then faithfully preserves — the
# first run had go writing its build cache to /root/.cache and failing). Where any of that is
# missing — no systemd, no sudo, no cgroup v2 — the command runs uncapped and
# says so once on stderr, so a self-hosted runner still passes the bars.
#
# Callers pass `CAP=` to make so the Makefile does not try to nest its user
# scope inside this one (a user-manager scope would move the process into
# user.slice, out of this cgroup, and the cap would silently stop applying).
#
#   CI_CAP_MEM   MemoryMax for the scope (default 10G); swap is refused
#                (MemorySwapMax=0) so an overrun is an OOM kill of the step,
#                not minutes of thrashing that end in a timeout.
#   CI_CAP_CPU   CPUQuota (default 300%: three of a hosted runner's four
#                cores, one left for the runner agent and the log shipper).
set -euo pipefail

mem="${CI_CAP_MEM:-10G}"
cpu="${CI_CAP_CPU:-300%}"

if command -v systemd-run >/dev/null 2>&1 &&
	[ -f /sys/fs/cgroup/cgroup.controllers ] &&
	sudo -n true 2>/dev/null; then
	exec sudo -n systemd-run --scope --quiet --collect \
		-p MemoryMax="$mem" -p MemorySwapMax=0 -p CPUQuota="$cpu" -- \
		sudo -n -E -u "$(id -un)" -- env PATH="$PATH" HOME="$HOME" "$@"
fi

echo "ci-cap: no capped systemd scope available here — running uncapped: $*" >&2
exec "$@"
