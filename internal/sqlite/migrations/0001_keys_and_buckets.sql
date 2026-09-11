CREATE TABLE api_keys (
  id          TEXT      NOT NULL PRIMARY KEY,
  secret      TEXT      NOT NULL,
  disabled    INTEGER   NOT NULL DEFAULT 0,
  created_at  INTEGER  NOT NULL
) WITHOUT ROWID, STRICT;

CREATE TABLE buckets (
  name        TEXT      NOT NULL PRIMARY KEY,
  public      INTEGER   NOT NULL DEFAULT 0,
  created_at  INTEGER   NOT NULL
) WITHOUT ROWID, STRICT;
