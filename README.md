# Prunelle

Review a feature, not merely its changed lines.

Prunelle is a feature-review tool: a Go review server and a desktop client. A review spans several Git repositories and is navigated by topics (intended behavior, observed changes, findings), each viewable as text, diagram, code diff, or evidence.

Status: early prototype. See [docs/plan.md](docs/plan.md) and the [spec](docs/Prunelle_Architecture_Implementation_Spec_2026-11-05.md).

## Layout

- `server/` - Go review server and `prunelle` CLI
- `docs/` - spec, delivery plan, decisions, per-milestone plans

## Development

Requires Go (see `server/go.mod`) and Git.

```sh
cd server
go test ./...
go generate ./api   # after editing api/openapi.yaml
go build -o bin/prunelle ./cmd/prunelle
```

Configuration lives in `<user config dir>/prunelle/config.toml` (`~/.config/prunelle/config.toml` on Linux):

```toml
listen = "127.0.0.1:7390"         # loopback only
allowed_roots = ["~/code"]        # repositories must live below one of these
allowed_logins = []               # Tailscale logins besides the machine owner
# data_dir defaults to ~/.local/share/prunelle (Linux) or ~/Library/Application Support/Prunelle (macOS)
```

Run the server and seed the demo review:

```sh
bin/prunelle serve
bin/prunelle fixture ~/code/prunelle-demo     # must be inside allowed_roots
bin/prunelle review ls
```

Expose it to your tailnet (no sudo once your user is the Tailscale operator):

```sh
tailscale serve --bg --https=443 http://127.0.0.1:7390
```

Requests through Tailscale Serve must come from the machine's Tailscale owner or a login in `allowed_logins`. Local processes on the server machine are trusted (see [decisions](docs/decisions.md), D2).

The API contract is [`server/api/openapi.yaml`](server/api/openapi.yaml). Review content is imported as a manifest: `prunelle review import REVIEW_ID manifest.json`; `prunelle fixture --print-manifest` prints a complete example.

## License

MIT
