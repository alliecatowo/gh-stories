<h1 align="center">GitHub Stories</h1>
<p align="center"><strong>Stories for GitHub. Yes, those Stories.</strong></p>
<p align="center">
Photos, videos, selfies, concerts, cats, food, memes, screenshots, shitposts.<br/>
They expire after 24 hours. They appear on avatars you already know — and in your terminal.
</p>

---

GitHub already has identities, avatars, follows, profiles and a dashboard. GitHub
Stories adds a Story ring to those avatars. Clicking a ring on a pull request
opens someone's concert video; closing it puts you back in the code review.
Running `gh stories` shows the same Story inside an actual terminal.

> This is an independent project. It is not affiliated with, endorsed by, or
> sponsored by GitHub.

## Install

**Browser extension** and **CLI** artifacts are published on the
[latest release](https://github.com/alliecatowo/gh-stories/releases/latest).

```bash
gh extension install alliecatowo/gh-stories
gh stories setup     # explains and offers: gh alias set story stories
gh stories login
gh stories
```

`gh stories` is the canonical command namespace. Everything documented works
under `gh stories` with no setup. `gh stories setup` additionally offers the
singular alias so `gh story post cat.jpg` works; it never clobbers an existing
alias or a real `gh` command.

Full instructions, the platform/renderer support matrix and a sample demo:
**https://alliecatowo.github.io/gh-stories/**

## What it does

| | |
|---|---|
| **Identity** | Real GitHub OAuth. Account switching, logout, revocable Stories sessions. |
| **Social graph** | Opt-out import of who you follow on GitHub, then independent Stories follows. Unfollow, mute, block. |
| **Stories** | Image and video uploads with captions and descriptions. Each item expires 24 hours after *it* was published. |
| **Privacy** | People I follow · My followers · Mutuals · Custom list · Public. Hide from specific people; turn replies and reactions off. |
| **Browser** | Dashboard row, avatar rings across GitHub, viewer, composer, inbox, settings, and a toolbar that works even if the page integration breaks. |
| **Terminal** | A full-screen TUI that renders the actual picture, posts, replies, reacts, and shows who viewed. |

Both clients talk to the same service, so read state, follows, privacy, replies,
reactions and deletions stay in sync.

## Develop

[mise](https://mise.jdx.dev) is the entry point. See
[docs/prerequisites.md](docs/prerequisites.md) for the things mise cannot
install for you (a container engine, ffmpeg, a graphical terminal).

```bash
mise install          # pinned Go, Node, pnpm
mise run doctor       # what's present, what's missing, what's misconfigured
mise run bootstrap    # dependencies + local configuration
mise run demo         # full local product with sample accounts and media
```

Other tasks: `mise tasks`.

## Documentation

- [Architecture](docs/architecture.md)
- [Privacy and retention](docs/privacy.md)
- [Self-hosting runbook](docs/runbook.md)
- [Support matrix](docs/support-matrix.md)
- [Acceptance matrix](docs/acceptance-matrix.md)
- [Visual verification report](evidence/visual-report.md)
- [Security policy](SECURITY.md) · [Contributing](CONTRIBUTING.md) · [Changelog](CHANGELOG.md)

## License

MIT — see [LICENSE](LICENSE).
