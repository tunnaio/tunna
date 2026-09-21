# ADR-0013: Presigned requests, the unsigned body, and a presign provider

**Status:** Accepted
**Date:** 2026-09-21 (drafted 2026-09-20)
**Deciders:** maintainer

## Context

A browser page should be able to use the SDK without holding a key: upload
large files with the concurrent uploader, put and read objects in private
buckets, all through URLs its own backend presigns. ADR-0010 listed this as
a revisit ("presign provider"); the first consumer, a management console,
asked for it.

Designing it surfaced a gap in what a presigned URL authorizes.

**A presigned URL binds the method, the path, the query and the listed
headers. It does not bind the body** (ADR-0003: the body is not signed,
because it streams). For an object `PUT` that is right: the path names the
destination, and the content is the uploader's to choose. For a route whose
*parameters* are a JSON body it is not. Probe, 2026-09-20, against the
fixture server: one presigned `POST /-/uploads` URL, minted with no
knowledge of any body, created a session in `photos` and then another in
`empty`, with a key and part size chosen by whoever held the URL. The same
holds for bucket create and patch and for key create and patch.

The route table in `spec/wire.md` section 4 already marks these routes
"key" and only the part, query, complete and abort routes "key or
presigned". The server does not enforce the column, and section 6 says
"every route below requires credentials, header form or presigned", which
contradicts it. So far the only party who could mint such a URL was the
key's owner, for themselves. A presign provider changes that: a backend
mints URLs for pages it does not trust, believing each one authorizes one
request.

A signed `X-Tunna-Checksum` does not close the gap. CRC32C detects
accidents; anyone can construct a different body with the same value. It
is integrity, not authorization.

Forces:

- **A backend must be able to reason about what it signs.** "This URL lets
  the holder do exactly this" has to be true, or the provider is a way to
  hand out more than intended.
- **One code path in the SDK.** The request pipeline signs today; with a
  provider it asks for a URL instead. Anything more is a second client.
- **The fast upload stays fast.** Parts go out concurrently with
  read-ahead (ADR-0010, measured 2026-09-17). A provider round trip per
  part must not bring back the lockstep that read-ahead removed.
- **Policy belongs to the application.** Which user may write which key is
  not tunna's business; the backend decides before it signs.

## Options considered

### Part 1: what the server accepts in presigned form

**S1: Enforce the table. The presigned form is refused on routes whose
parameters are in a JSON body:** session initiate, bucket create and patch,
key create and patch. Accepted everywhere else: object routes, the part,
query, complete and abort routes of a session, and reads of the control
plane. `complete` stays accepted although it has a body: `parts` and
`checksums` describe bytes the same holder uploaded, and refusing it would
make a browser upload impossible to finish.

**S2: Bind the body with a cryptographic hash in a signed header
(SHA-256), required on presigned requests with a body.** Closes the gap
everywhere and keeps every route presignable. But the minter must then know
the exact body in advance, which for session initiate means the backend
composes the JSON and the page replays it byte for byte: more moving parts
than the backend making that one small call itself. And it adds a second
hash next to CRC32C (ADR-0006) for the control plane only.

**S3: Leave it and document it.** "A presigned URL for a body route
authorizes any body." True, and a trap: the natural reading of "presigned
`POST /-/uploads`" is "may start *this* upload".

### Part 2: the provider's shape

**P1: One request in, one URL out.**

```ts
type PresignProvider = (request: PresignableRequest, options: CallOptions) => Promise<string>;
new Tunna({ url, presign: provider });          // instead of key
```

`PresignableRequest` is what the pipeline would otherwise sign: method,
path segments, query, and the headers to bind. The pipeline calls the
provider where it calls the signer today. In `upload`, the call happens
inside `prepare()`, next to the read and the hash, so it overlaps with the
part in flight and costs no wall-clock time.

**P2: A batch: requests in, URLs out, same order.** One backend round trip
for a window of parts. But a part's URL binds its checksum header, which is
known only once the part is read, so a batch ahead of time would have to
leave the checksum unsigned, and the provider's backend endpoint becomes a
list endpoint. It optimizes a cost that P1 already hides.

**P3: No provider; a helper that uploads to URLs the caller supplies.**
Covers upload only; `objects.get` on a private bucket, `head`, `delete`
and `list` would each need their own variant.

**P4: The provider as a class or interface rather than a function.** It has
one operation and no state; the SDK cannot ship an implementation, since
every application's endpoint, authentication and policy differ. A function
is what `fetch` already is in `TunnaOptions`, is one line for an
application to supply, and composes (caching, logging, retries are
functions around it). A later batch method would be a second optional
option either way.

**P5: A separate client class for provider mode**, without the methods a
page cannot use, and with `session` a required parameter of `upload`.
More honest at the type level. But only one method needs a runtime check
in the SDK (`upload` without a session); everything else that cannot work
is refused by the server, or by the application's provider, which may
refuse more (a provider can decline `objects.delete`), and no class can
express an application's policy. The two modes share nearly every method,
so a second class is mostly a second copy of the documentation. Adding a
narrower façade later breaks nobody; merging two public classes would.

### Part 3: who initiates an upload session

Follows from part 1: a page cannot, in any option but S2.

**U1: The backend creates the session with its key and hands the page the
session record; the page uploads into it.**
`tunna.upload(bucket, key, source, { session })` skips the create, takes
the part size from the record, and asks the provider for part, complete
and abort URLs under that session id. The backend's policy is one check:
"did I create this session for this user".

**U2: S2's replayed body.** See S2.

## Trade-off analysis

S1 is the smallest change that makes the sentence "a presigned URL
authorizes exactly one request" true, and it is what the contract already
says. It costs one capability nobody has asked for (presigned bucket or
key management) and moves session initiate to the backend, where the
policy decision lives anyway: choosing bucket, key and part size *is* the
authorization. S2 buys generality with a second hash and a replay
protocol. S3 leaves a trap under the feature being built.

P1 keeps one pipeline. Its cost, a round trip per part, is real on paper
(350 calls for a 2.8 GB file at 8 MiB) and invisible in practice: with
read-ahead each worker prepares part n+1 while n is in flight, and the
provider call joins the read and the hash in that slot. If a measurement
ever shows the provider on the critical path, P2 is an addition, not a
break: a provider may be given a second, optional batch method.

## Decision

**S1, P1 and U1.**

### Server

The presigned form is refused on `POST /-/uploads`, `PUT` and `PATCH
/-/buckets/{bucket}`, `POST /-/keys` and `PATCH /-/keys/{id}`. Proposed
answer: a new stage 2 code, `presign_not_allowed` (401), "this route takes
its parameters from a body a presigned URL cannot bind; sign it in header
form". Stage 2, because it is a statement about the credentials presented,
before anyone asks what the key may do. `spec/wire.md` section 6 is
corrected to agree with the table, and section 3.4 states the rule and its
reason.

### SDK, backend half

`presignRequest({ method, path, query?, headers?, expiresIn })` returns a
URL on `publicUrl`. It is the general form; `presign({ bucket, key, ... })`
becomes a call to it. It refuses, with a `TypeError`, the routes the server
refuses, so a backend finds out when it writes the code.

### SDK, page half

`new Tunna({ url, presign })`; `key` and `presign` together is a
`TypeError`. The pipeline asks the provider for a URL where it would have
signed, passing the caller's `signal`. `health`, `version` and `limits`
stay unsigned and never reach the provider. `tunna.presign(...)` in
provider mode returns the provider's URL, for an `<img src>`.

`upload` takes `{ session }`: an `UploadSession` the backend created.
Without a key and without a session it is a `TypeError` that says to
create the session on the backend. An error thrown by the provider
propagates unchanged, like an abort reason; it is the application's own.

The provider is not given an expiry. How long a URL lives is the
backend's decision.

### What a page still cannot do

Create or patch buckets, manage keys, initiate a session. By construction,
not by omission: those are the requests whose meaning a URL cannot carry.

## Consequences

- A backend can state its policy as "I sign paths under sessions I
  created, and object paths under this user's prefix", and that is the
  whole of what the page can do.
- One new error code; `errors.json`, the generated SDK table and the
  stage vectors gain a row.
- A client that presigned a bucket create or a session initiate for itself
  breaks. None is known; the SDK never offered it (`presign` takes a bucket
  and a key).
- `upload` in provider mode makes one backend call per part. Hidden by
  read-ahead; to be measured against the 2026-09-17 numbers.

To revisit:

- A batch method on the provider, if a measurement shows the per-part call
  on the critical path.
- A separate, narrower client class for provider mode (P5), if the list of
  methods that cannot work there grows, or users keep meeting the runtime
  error: then the types are lying often enough to matter.
- Whether `complete` should bind `parts` in the query, so that even the
  count is part of the signature.
- Resuming a session after a page reload; the `{ session }` option is
  already the shape that needs.

## Action items

1. [x] Maintainer accepts, amends, or rejects this record. Accepted 2026-09-21: S1 with the new code, P1 as a function named `presign`, U1, one client class.
2. [ ] `spec/errors.json`: `presign_not_allowed`; `spec/wire.md` 3.4, 4 and
       6; conformance cases for each refused route and for `complete`
       staying accepted.
3. [ ] Server: refuse the presigned form on the five routes.
4. [ ] SDK tests: `presignRequest`, provider mode in the pipeline, `upload`
       with `{ session }`, the two `TypeError`s.
5. [ ] SDK: the code, by the maintainer.
6. [ ] README: a backend endpoint and a page, end to end, in twenty lines.
