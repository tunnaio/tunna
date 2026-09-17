# Configuration

The server reads its configuration from environment variables with the
`TUNNA_` prefix. Every effective value is logged at startup, secrets
redacted, because container tooling forwards only the variables it is told
to and that log line is the only way to see what arrived.

An unset or empty variable takes its default. A variable with no default is
required, and the server refuses to start without it.

| Variable | Default | Meaning |
|----------|---------|---------|
| `TUNNA_ADDR` | `:8000` | Listen address, `host:port`. An empty host means every interface. When unset, a platform-style `PORT` variable is honoured as `:PORT`; `TUNNA_ADDR` wins when both are set. A `PORT` that is not a number from 1 to 65535 is a startup error. |
| `TUNNA_DATA_DIR` | required | Directory for the metadata database and object files. Created if absent. |
| `TUNNA_BOOTSTRAP_KEY` | none | `<id>:<secret>`. At startup the key is inserted, or updated and re-enabled if it exists. See below. |
| `TUNNA_CORS_ORIGINS` | none | Comma-separated browser origins allowed by CORS: exact (`https://app.example.com`), one-label wildcard (`https://*.example.com`), or `*`. Empty means no CORS headers at all. See below. |

## Bootstrap key

A fresh server has no API keys, and every route except health and version
requires one. `TUNNA_BOOTSTRAP_KEY` is how the first key gets in. It is an
**admin** key (ADR-0008) named `bootstrap`; use it to create the managed
keys through `POST /-/keys`, then remove the variable.

- The value is split on the **first** colon. The id may not contain a colon;
  the secret may.
- Both halves must be non-empty. Anything else is a startup error that names
  the variable.
- The key is upserted on every start while the variable is set: the secret
  is set to the value given, `admin` is set, `disabled` is cleared, the name
  becomes `bootstrap`. A disabled, rotated or de-admined key with that id
  is restored. That is deliberate: it is the documented recovery from a
  lockout, since the server does not stop an admin from disabling or
  deleting the last admin key (ADR-0008).
- Only the id is logged.
- Remove the variable once a managed admin key exists. A secret in a process
  environment is readable by anything that can inspect the process, which
  is acceptable for bootstrap and wrong as a permanent arrangement.

## CORS origins

`TUNNA_CORS_ORIGINS` is one list for the whole server (ADR-0009). It is
not a security boundary: every request carries an explicit signature or a
presigned query and a browser attaches nothing on its own, so the list
only decides which pages a browser lets read the responses, and keeps
caches from serving one origin's answer to another.

- Entries are separated by commas; surrounding whitespace and empty
  entries are ignored, so a trailing comma is harmless.
- Each entry is `*`, an exact origin `scheme://host[:port]`, or a pattern
  `scheme://*.domain[:port]` whose `*` stands for exactly one DNS label.
  `https://*.example.com` allows `https://app.example.com` and not
  `https://example.com` or `https://a.b.example.com`.
- Scheme is `http` or `https`. Entries are lowercased and a default port
  is dropped, which is how browsers send `Origin`; `https://App.Example.com:443`
  and `https://app.example.com` are the same entry.
- A value that is not one of those shapes, a path, a wildcard anywhere but
  the leftmost label, a bare `https://*`, is a startup error naming the
  variable. The whole variable is refused, so a typo cannot silently drop
  one origin.
- The effective list is logged at startup.
- Local development against a dev server on another port needs the page's
  origin with its port, for example `http://localhost:5173`. A page served
  over `https` may call an `http://localhost` origin; browsers exempt it
  from mixed-content blocking.
