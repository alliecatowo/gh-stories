# Contributing

Thanks for looking. This project is small and opinionated; that keeps it
maintainable by one person.

## Getting set up

```bash
mise install
mise run doctor      # tells you exactly what is missing
mise run bootstrap
mise run demo
```

`docs/prerequisites.md` lists the system packages mise does not manage.

## Before you open a pull request

```bash
mise run check       # format, lint, types, contract, static analysis
mise run test        # unit + real-service integration tests
```

CI runs the same mise tasks with the same pinned tool versions.

## What belongs in this product

Every proposed addition has to pass one question:

> Would an ordinary Stories product have this?

If the justification starts with "developers could use it to…", the answer is
no. Posting is posting. Following is following. A reply is a reply. We do not
rename ordinary social actions into Git operations, and there is no
developer-specific content format.

Explicitly out of scope for now: organization posting, bot posting,
recommendations, an Explore feed, archives, Highlights, live streaming, music,
filters, ads, analytics, verified badges, AI-generated Stories, and anything
that posts automatically from repository activity.

## Reporting bugs

Use the issue templates. For anything security related, read
[SECURITY.md](SECURITY.md) first — please do not open a public issue.
