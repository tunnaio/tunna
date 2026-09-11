# Configuration

The server reads its configuration from environment variables with the
`TUNNA_` prefix. Every effective value is logged at startup, secrets
redacted, because container tooling forwards only the variables it is told
to and that log line is the only way to see what arrived.

An unset or empty variable takes its default. A variable with no default is
required, and the server refuses to start without it.

| Variable | Default | Meaning |
|----------|---------|---------|
| `TUNNA_ADDR` | `:8000` | Listen address, `host:port`. An empty host means every interface. |
| `TUNNA_DATA_DIR` | required | Directory for the metadata database and object files. Created if absent. |
| `TUNNA_BOOTSTRAP_KEY` | none | `<id>:<secret>`. At startup the key is inserted, or updated and re-enabled if it exists. See below. |

## Bootstrap key

A fresh server has no API keys, and every route except health and version
requires one. `TUNNA_BOOTSTRAP_KEY` is how the first key gets in until key
management exists (a follow-up to ADR-0003, decided with the authorization
model).

- The value is split on the **first** colon. The id may not contain a colon;
  the secret may.
- Both halves must be non-empty. Anything else is a startup error that names
  the variable.
- The key is upserted on every start while the variable is set. A disabled
  key with that id becomes enabled again. That is deliberate: an operator
  locked out of a deployment recovers by restarting with the variable set.
- Only the id is logged.
- Remove the variable once a managed key exists. A secret in a process
  environment is readable by anything that can inspect the process, which
  is acceptable for bootstrap and wrong as a permanent arrangement.
