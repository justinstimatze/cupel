# Security Policy

## Reporting a vulnerability

Email **justin@justinstimatze.com** with details rather than opening a public
issue or PR. I'll acknowledge receipt within 7 days and aim to provide an initial
assessment within 30 days; we can coordinate a disclosure timeline, defaulting to
90 days from the initial report unless circumstances warrant otherwise.

## Threat model

cupel has two surfaces, both local:

- A Claude Code `UserPromptSubmit` hook (`cupel hook`) that reads the prompt
  from stdin, embeds it against a local model (ollama), optionally consults the
  Anthropic API, and writes calibration logs under `~/.claude/cupel/` (mode
  `0600`). The hook **never executes prompt content** — a crafted prompt can at
  most cause a cosmetic mis-fire of the advisory message; it cannot run code,
  read arbitrary files, or block the turn. Every error path fails open and exits
  cleanly.
- A static-site generator and local preview server (`cupel render` /
  `cupel serve`). `serve` binds `localhost` only and serves rendered Markdown by
  slug lookup — it does not serve arbitrary files from disk.

## Secrets

The Anthropic API key is read from the environment or a local, gitignored
`.env`; it is never logged and never committed. No secrets are stored in the
repository.
