## Install

```bash
gh extension install alliecatowo/gh-stories
gh stories setup      # offers the singular `gh story` alias
gh stories login
gh stories
```

The browser extension ZIPs below are **manual installs**. They are not store
listings: load the Chromium ZIP through `chrome://extensions` with developer
mode on, and the Firefox build as a temporary add-on. Persistent signed
installs require store review, which has not happened yet.

Service image:

```bash
docker pull ghcr.io/alliecatowo/gh-stories:latest
```

Verify downloads against `checksums.txt`.
