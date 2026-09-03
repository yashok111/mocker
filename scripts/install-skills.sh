#!/usr/bin/env bash
# Install mocker's curated agent skills at the PROJECT (repo-root) level, into
# BOTH local agents:
#   - Claude Code      -> <repo>/.claude/skills/   (root-scoped: this is the only
#     place the Claude Code Skill tool reads — per-package subdirs are NOT seen)
#   - Codex/universal  -> <repo>/.agents/skills/
# Both dirs are gitignored; skills-lock.json (repo root) is tracked — re-run this
# script after a fresh clone to repopulate (NOT `skills experimental_install`,
# which only restores .agents/ and leaves Claude Code's copies missing).
#
# CLI note (skills v2): a single `-a` does NOT reliably accept multiple agents
# (`-a claude-code codex` has been observed keeping only the last; comma is
# rejected). So we loop and call `skills add` once per agent — correct whichever
# way the CLI behaves, at the cost of one extra pass.
#
# Usage (run from anywhere):
#   bash scripts/install-skills.sh                 # install every group
#   bash scripts/install-skills.sh --dry-run       # print the commands only
#   bash scripts/install-skills.sh golang jetbrains  # only the named group(s)
set -euo pipefail

# Agents to install into. Each gets its own `skills add` pass (see CLI note).
AGENTS=(claude-code codex)

# --- Curated sources --------------------------------------------------------
REPO_SUPERPOWERS="https://github.com/obra/superpowers"
REPO_GOLANG="https://github.com/samber/cc-skills-golang"
REPO_NETRESEARCH="https://github.com/netresearch/go-development-skill"
REPO_JETBRAINS="https://github.com/JetBrains/go-modern-guidelines"
REPO_ECC="https://github.com/affaan-m/everything-claude-code"
REPO_WSHOBSON="https://github.com/wshobson/agents"
REPO_MATTPOCOCK="https://github.com/mattpocock/skills"
REPO_ANTHROPICS="https://github.com/anthropics/skills"
# Added 2026-09-03 to cover the rest of the stack (the owner's ask: "what
# else on skills.sh covers us"). Each is the library's own or a maintainer
# with a track record; single-star repos were passed over on the
# supply-chain note above.
REPO_TANSTACK="https://github.com/DeckardGer/tanstack-agent-skills"
REPO_MANTINE="https://github.com/mantinedev/skills"
REPO_DOCKER="https://github.com/netresearch/docker-development-skill"
REPO_VERCEL="https://github.com/vercel-labs/agent-skills"

# obra/superpowers — stack-agnostic dev-workflow discipline.
SUPERPOWERS_SKILLS=(
  brainstorming using-superpowers writing-plans executing-plans
  test-driven-development systematic-debugging verification-before-completion
  requesting-code-review receiving-code-review subagent-driven-development
  dispatching-parallel-agents using-git-worktrees
)
# samber/cc-skills-golang — the server is Go: stdlib net/http + database/sql over
# SQLite, hand-wired. Deliberately EXCLUDED: cobra/viper (no CLI), grpc/graphql
# (out of scope per DESIGN §2), swaggo (we consume OpenAPI, we don't annotate it),
# uber-fx/dig/wire (no DI container), samber-* helper libs.
GOLANG_SKILLS=(
  golang-code-style golang-naming golang-project-layout golang-documentation
  golang-error-handling golang-context golang-concurrency
  golang-structs-interfaces golang-design-patterns golang-database
  golang-testing golang-stretchr-testify golang-lint golang-modernize
  golang-safety golang-security golang-troubleshooting golang-observability
  golang-dependency-management golang-continuous-integration
)
# netresearch/go-development-skill — bundled enterprise Go patterns.
NETRESEARCH_SKILLS=(go-development)
# JetBrains/go-modern-guidelines — modern Go idioms (auto-detects go.mod).
JETBRAINS_SKILLS=(use-modern-go)
# affaan-m/everything-claude-code — API/arch/security + the P1 admin UI.
ECC_SKILLS=(
  api-design architecture-decision-records security-review security-scan
  safety-guard documentation-lookup search-first codebase-onboarding
  git-workflow e2e-testing browser-qa context-budget
  frontend-patterns frontend-design-direction design-system accessibility
)
# wshobson/agents — the admin UI is React + Vite + TanStack Query (DESIGN §14).
# nextjs-* deliberately excluded: no Next here.
WSHOBSON_SKILLS=(
  typescript-advanced-types react-state-management
  javascript-testing-patterns error-handling-patterns
)
# mattpocock/skills — TDD/debug companions + harsher self-review.
MATTPOCOCK_SKILLS=(tdd diagnosing-bugs grill-me improve-codebase-architecture)
# anthropics/skills — skill-creator, to author project-convention skills;
# mcp-builder for the MCP server that lets an agent drive mocker's admin API
# (the seventh P2 slice: one binary, stdio transport, no new auth surface).
ANTHROPICS_SKILLS=(skill-creator mcp-builder)
# DeckardGer/tanstack-agent-skills — the SPA routes with TanStack Router
# (file-based, codegen) and fetches with TanStack Query over the orval
# client. start/integration excluded: no TanStack Start here.
TANSTACK_SKILLS=(tanstack-query-best-practices tanstack-router-best-practices)
# mantinedev/skills — Mantine 9 is the whole component layer; forms are
# react-hook-form + arktype, but every editor renders Mantine inputs and
# the Styles API. combobox excluded: no custom select in the panel.
MANTINE_SKILLS=(mantine-form mantine-custom-components)
# netresearch/docker-development-skill — the distroless image, the compose
# stack and the TLS overlay (A5). docker-via-wsl excluded: Linux host.
DOCKER_SKILLS=(docker-development)
# vercel-labs/agent-skills — React render/bundle rules; the Next-only ones
# do not fire without Next.
VERCEL_SKILLS=(vercel-react-best-practices)

DRY_RUN=0
ALL_GROUPS=(superpowers golang netresearch jetbrains ecc wshobson mattpocock anthropics tanstack mantine docker vercel)
TARGETS=("${ALL_GROUPS[@]}")
SELECTED=()
for arg in "$@"; do
  case "$arg" in
    --dry-run|-n) DRY_RUN=1 ;;
    superpowers|golang|netresearch|jetbrains|ecc|wshobson|mattpocock|anthropics|tanstack|mantine|docker|vercel)
      SELECTED+=("$arg") ;;
    -h|--help) sed -n '2,19p' "$0"; exit 0 ;;
    *) echo "unknown arg: $arg" >&2; exit 2 ;;
  esac
done
[[ ${#SELECTED[@]} -gt 0 ]] && TARGETS=("${SELECTED[@]}")

run() {  # echo the command (safely quoted), then run it unless --dry-run
  printf '+'; printf ' %q' "$@"; printf '\n'
  [[ $DRY_RUN -eq 0 ]] && "$@"
  return 0
}

install_set() {
  local repo="$1"; shift
  local -a skills=("$@")
  echo; echo "=== $repo (${#skills[@]} skills) -> ${AGENTS[*]} ==="
  # SUPPLY CHAIN: `npx -y -p skills` runs an unpinned third-party package, and the
  # repos are third-party — i.e. arbitrary code executed as you; installed skills
  # run with full agent permissions. Only trusted sources; review SKILL.md. Harden
  # by pinning the package (-p skills@X.Y.Z) and each repo to a commit SHA.
  local agent
  for agent in "${AGENTS[@]}"; do   # one pass per agent (see CLI note)
    run npx -y -p skills skills add "$repo" -y -a "$agent" --skill "${skills[@]}"
  done
}

cd "$(dirname "$0")/.."   # repo root: project-scoped .claude/skills + .agents/skills
for t in "${TARGETS[@]}"; do
  case "$t" in
    superpowers) install_set "$REPO_SUPERPOWERS" "${SUPERPOWERS_SKILLS[@]}" ;;
    golang)      install_set "$REPO_GOLANG"      "${GOLANG_SKILLS[@]}" ;;
    netresearch) install_set "$REPO_NETRESEARCH" "${NETRESEARCH_SKILLS[@]}" ;;
    jetbrains)   install_set "$REPO_JETBRAINS"   "${JETBRAINS_SKILLS[@]}" ;;
    ecc)         install_set "$REPO_ECC"         "${ECC_SKILLS[@]}" ;;
    wshobson)    install_set "$REPO_WSHOBSON"    "${WSHOBSON_SKILLS[@]}" ;;
    mattpocock)  install_set "$REPO_MATTPOCOCK"  "${MATTPOCOCK_SKILLS[@]}" ;;
    anthropics)  install_set "$REPO_ANTHROPICS"  "${ANTHROPICS_SKILLS[@]}" ;;
    tanstack)    install_set "$REPO_TANSTACK"    "${TANSTACK_SKILLS[@]}" ;;
    mantine)     install_set "$REPO_MANTINE"     "${MANTINE_SKILLS[@]}" ;;
    docker)      install_set "$REPO_DOCKER"      "${DOCKER_SKILLS[@]}" ;;
    vercel)      install_set "$REPO_VERCEL"      "${VERCEL_SKILLS[@]}" ;;
  esac
done
echo
echo "done. skills in .claude/skills/ (Claude Code) + .agents/skills/ (Codex)."
echo "lockfile: skills-lock.json (commit it; restore by re-running this script)."
echo "NOTE: restart Claude Code to pick up newly added skills."
