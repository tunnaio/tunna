# Contributing

Thanks for looking. A few things about how this project is run, so that
your time is well spent.

## What this project is

tunna is built by one maintainer, and it is deliberately also a learning
project: the server is the maintainer's first Go, and each SDK is a bounded
project in its own language. That has one consequence for contributions:
**feature code is generally written by the maintainer.** Pull requests that
implement a feature may be declined, with thanks, even when they are good,
because writing it is the point.

What is welcome, and where a pull request will be taken seriously:

- **Bug reports with a reproduction.** Best of all is a failing conformance
  case or vector: a JSON entry in `spec/` that the server or an SDK gets
  wrong is a report the tests can run.
- **Spec problems.** An ambiguity in `spec/wire.md`, a vector that
  contradicts the prose, an error code with no case pinning it.
- **Documentation fixes** and anything that makes the decision records
  clearer.
- **A new SDK in a language that has none**, if you want to own it. Open an
  issue first so the layout and the conformance setup are agreed.
- **Security reports**, through [`SECURITY.md`](SECURITY.md).

## How things are decided

Every non-obvious choice has a record in `docs/decisions/` with the
alternatives it beat. If you disagree with a decision, the record is the
place to argue with; an issue that says which record and what changed is
the way to open it. A change to the wire contract needs a spec change,
vectors or cases for it, and a record if it is a design change, before any
implementation.

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
