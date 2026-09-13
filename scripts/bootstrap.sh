#!/usr/bin/env bash
# bootstrap.sh — install project + browser test dependencies, and prepare
# local configuration. Idempotent: safe and reasonably fast to run twice.
set -euo pipefail
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib.sh"

ghs_step "pnpm install"
if ghs_mise pnpm install --frozen-lockfile 2>/tmp/ghs-pnpm-install.log; then
  ghs_ok "pnpm install (frozen lockfile)"
else
  if grep -q ERR_PNPM_OUTDATED_LOCKFILE /tmp/ghs-pnpm-install.log; then
    ghs_warn "pnpm-lock.yaml is behind package.json changes (expected during active development) — installing and updating the lockfile"
    ghs_mise pnpm install
  else
    cat /tmp/ghs-pnpm-install.log >&2
    ghs_die "pnpm install failed"
  fi
fi

ghs_step "go mod download"
ghs_mise go mod download

ghs_step ".env"
ENV_FILE="$GHS_ROOT/.env"
ENV_EXAMPLE="$GHS_ROOT/.env.example"
if [ -f "$ENV_FILE" ]; then
  ghs_ok ".env already exists — leaving it alone"
else
  [ -f "$ENV_EXAMPLE" ] || ghs_die ".env.example is missing; cannot bootstrap .env"
  cp "$ENV_EXAMPLE" "$ENV_FILE"
  chmod 600 "$ENV_FILE"
  ghs_ok "created .env from .env.example"

  ghs_step "generating a real GHS_SECRET_KEY"
  SECRET="$(openssl rand -base64 48 2>/dev/null | tr -d '\n')"
  if [ -z "$SECRET" ]; then
    ghs_die "openssl is required to generate GHS_SECRET_KEY (openssl rand -base64 48 produced no output)"
  fi
  # Portable in-place edit (macOS/BSD sed requires an explicit backup suffix).
  tmp="$(mktemp)"
  sed "s#^GHS_SECRET_KEY=.*#GHS_SECRET_KEY=${SECRET}#" "$ENV_FILE" > "$tmp"
  mv "$tmp" "$ENV_FILE"
  chmod 600 "$ENV_FILE"
  ghs_ok "wrote a random 48-byte GHS_SECRET_KEY into the new .env (value not printed)"
fi

ghs_step "Playwright browsers"
if [ -d "$GHS_ROOT/tests/e2e" ]; then
  PW_DIR="$GHS_ROOT/tests/e2e"
  case "$(uname -s)" in
    Linux)
      # --with-deps installs OS package dependencies and needs root on most
      # distros. Try it; if it fails because we're unprivileged, fall back to
      # installing just the browsers and tell the human what's missing (see
      # docs/prerequisites.md for the manual package list).
      if [ "$(id -u)" -eq 0 ]; then
        ghs_mise pnpm --dir "$PW_DIR" exec playwright install --with-deps chromium firefox
      elif ghs_mise pnpm --dir "$PW_DIR" exec playwright install --with-deps chromium firefox 2>/tmp/ghs-playwright-deps.log; then
        ghs_ok "playwright browsers + OS dependencies installed"
      else
        ghs_warn "playwright install --with-deps failed without root (see /tmp/ghs-playwright-deps.log) — installing browsers only"
        ghs_mise pnpm --dir "$PW_DIR" exec playwright install chromium firefox
        ghs_warn "OS-level browser dependencies may be missing; see docs/prerequisites.md for the manual apt package list, or re-run with sudo"
      fi
      ;;
    Darwin)
      # macOS has no --with-deps system package step; browsers only.
      ghs_mise pnpm --dir "$PW_DIR" exec playwright install chromium firefox
      ghs_ok "playwright browsers installed (macOS needs no extra OS packages)"
      ;;
    *)
      ghs_mise pnpm --dir "$PW_DIR" exec playwright install chromium firefox
      ;;
  esac
else
  ghs_warn "tests/e2e does not exist yet — skipping Playwright browser install (nothing to install for)"
fi

ghs_step "bootstrap complete"
ghs_ok "run 'mise run doctor' to verify, then 'mise run demo'"
