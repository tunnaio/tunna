# tunna

Object storage server in Go, with SDKs to follow. *Tunna* is Swedish for
barrel.

**Status:** early. Design is settled and written down; the server is being
built. Nothing here is usable yet.

## Goals

- Fast. Single static binary, one data directory, no second process to run.
- One wire contract, specified as data, that the server and every SDK are
  tested against.

## Layout

| Path | What |
|------|------|
| `docs/decisions/` | Architecture decision records. Every non-obvious choice, with the alternative it beat. |
| `spec/` | The wire contract, error table, signing and encoding vectors, and conformance cases. |
| `sig/`, `internal/`, `cmd/tunna` | The server. See ADR-0005 for the package layout. |
| `cmd/tunna-fixtures` | The API in the conformance fixture state, with `POST /reset`, for SDK conformance runners (ADR-0010). |
| `cmd/tunna-load` | Load generator; see `docs/benchmarks/`. |
| `sdk/` | Client SDKs, one directory per language. |

## Reading order

1. [`spec/README.md`](spec/README.md) for how the spec is versioned and used.
2. [`spec/wire.md`](spec/wire.md) for the contract itself.
3. [`docs/decisions/`](docs/decisions/) for why it looks the way it does.

## License

[MIT](LICENSE).
