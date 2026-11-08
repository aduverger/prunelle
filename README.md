# Prunelle

Review a feature, not merely its changed lines.

Prunelle is a feature-review tool: a Go review server and a desktop client. A review spans several Git repositories and is navigated by topics (intended behavior, observed changes, findings), each viewable as text, diagram, code diff, or evidence.

Status: early prototype. See [docs/plan.md](docs/plan.md) and the [spec](docs/Prunelle_Architecture_Implementation_Spec_2026-11-05.md).

## Layout

- `server/` - Go review server and `prunelle` CLI
- `docs/` - spec, delivery plan, decisions, per-milestone plans

## License

MIT
