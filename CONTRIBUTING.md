# Contributing

Thanks for looking. A few things about how this project is run, so that
your time is well spent.

## Before a feature

Every non-obvious choice has a record in `docs/decisions/` with the
alternatives it beat. A change to the wire contract or the design starts
as an issue naming the record it touches, or proposing a new one; the
implementation comes after the decision, and a spec change lands with its
vectors or conformance cases before any server or SDK code. Opening the
issue first avoids a pull request that argues with a settled decision.

Everyone taking part is bound by the [code of conduct](CODE_OF_CONDUCT.md).

## What is welcome

- **Bug reports with a reproduction.** Best of all is a failing conformance
  case or vector: a JSON entry in `spec/` that the server or an SDK gets
  wrong is a report the tests can run.
- **Spec problems.** An ambiguity in `spec/wire.md`, a vector that
  contradicts the prose, an error code with no case pinning it.
- **Documentation fixes** and anything that makes the decision records
  clearer.
- **A new SDK in a language that has none.** Open an issue first so the
  layout and the conformance setup are agreed; `sdk/typescript` is the
  reference for both.
- **Security reports**, through [`SECURITY.md`](SECURITY.md).

## Rules the repository enforces

- `gofmt`-clean, `go vet`-clean, tests green on Linux and Windows; the SDK
  type-checks and its generated files are current. CI checks all of it.
- Line endings are LF everywhere (`.gitattributes`); the editorconfig
  matches.
- Nothing in tracked files is specific to a person or a machine.
- Versions are pinned exactly.
- Commits are written by people. No AI attribution trailers.

## Running the tests

```
go test ./...                      # server, including the conformance suite
cd sdk/typescript && bun test      # SDK, spawns cmd/tunna-fixtures (needs Go)
```
