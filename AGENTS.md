# Repository Guidelines

## Project Structure & Module Organization
`main.go` boots the Discord bot, loads `.env`, opens `voltgpt.db`, and registers handlers. Keep feature code under `internal/` by domain: `handler/` for Discord events, `config/` for slash commands and prompts, `db/` for SQLite setup, `memory/`, `reminder/`, `gamble/`, `hasher/`, `utility/`, and `apis/` for OpenAI, Gemini, and Wavespeed clients. Store design notes in `docs/plans/`. Keep test fixtures next to the package that uses them, such as `internal/utility/testdata/`.

## Build, Test, and Development Commands
Use the standard Go toolchain from the repo root:

```bash
go build -o voltgpt        # build the bot binary
go run .                   # run locally; reads .env automatically
go test ./... -timeout 60s # full test suite; matches CI
go test ./internal/memory -run TestName -v -timeout 30s
go vet ./...               # static checks
```

CI in `.github/workflows/test.yml` runs `go test ./... -timeout 60s` on Ubuntu with `gcc` and `ffmpeg` installed, so keep changes compatible with CGO and media tests.

## Coding Style & Naming Conventions
Follow idiomatic Go. Format with `gofmt` before committing and keep imports organized by the Go toolchain. Use lowercase package names, exported `CamelCase`, unexported `camelCase`, and colocated `_test.go` files. Prefer small feature packages under `internal/` instead of growing `main.go`. This repo uses raw `database/sql`; keep SQL explicit rather than introducing an ORM.

## Testing Guidelines
Write table-driven tests where practical and keep tests beside the code they cover. Name tests `TestXxx` and favor focused package-level runs while iterating. Some integration-style tests skip when API tokens are absent, but the default suite should still pass with `go test ./... -timeout 60s`. If you touch media handling, remember video tests depend on `ffmpeg`.

## Commit & Pull Request Guidelines
Recent history follows a conventional pattern like `feat(openai): ...` or `fix(memory): ...`. Use `type(scope): imperative summary`, with scopes matching packages or features (`handler`, `memory`, `docs`). For pull requests, include a short behavior summary, list the commands you ran, link the issue if there is one, and attach screenshots or Discord message samples for user-facing changes.

## Configuration & Secrets
Start from `sample.env`; at minimum set `DISCORD_TOKEN`. Never commit real tokens, `.env`, or generated local data such as `voltgpt.db`.
