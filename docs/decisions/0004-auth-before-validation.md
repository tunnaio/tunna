# ADR-0004: Request processing order (authentication before validation)

**Status:** Accepted
**Date:** 2026-09-09
**Deciders:** maintainer

## Context

A request can be wrong in several ways at once: unsigned, signed by an unknown
key, aimed at a bucket that does not exist, carrying a part number out of
range, and too large, all in one message. The server answers with exactly one
status code. Which fault wins is a contract, not an implementation detail:
every conformance case with more than one fault needs a single expected
answer, and every SDK's error mapping depends on it.

The predecessor answered validation errors before authentication errors. That
was not a decision; its web framework ran schema hooks before route handlers,
and auth lived in a handler. S3 does the reverse. In Go with `net/http` there
is no framework imposing an order. The pipeline is code the maintainer writes,
so the order is chosen on the merits.

Forces:

- **Do not read a body from a caller you have not authenticated.** Part
  uploads are up to the proxy cap in size. Validating a body, or anything that
  requires consuming it, before checking the signature means spending
  bandwidth and disk on anonymous traffic.
- **Do not disclose to anonymous callers.** Whether a bucket exists, whether a
  key exists, what a session's part size is, what range a parameter accepts:
  none of it should be learnable without a valid signature. Existence checks
  are the sharp edge; syntax is not, because the wire contract is public.
- **The signature needs parsed inputs.** ADR-0003 signs method, canonical path,
  canonical query and listed headers. Those must be parsed and canonicalized
  before verification can run. A request whose query cannot be decoded cannot
  be verified at all, so some syntactic checking necessarily precedes auth.
- **Precedence must be total.** Two implementations, or one implementation on
  two days, must give the same answer to the same compound fault. The
  conformance suite should include compound-fault cases on purpose.
- **Cost.** Authentication is a cache lookup and one HMAC. Semantic validation
  ranges from free to reading the whole body. Cheap checks that protect
  expensive ones should run first.

## Options considered

### Option A: Authenticate first, then validate (S3 order)

Parse what the signature needs, verify the signature, authorize the caller on
the named resource, then validate everything else, then touch the body.

**Pros**

- Anonymous callers learn nothing beyond what the public spec says, and cost
  the server nothing beyond one HMAC.
- The body is never consumed before the caller is known. With
  `Expect: 100-continue` from the client, a rejected upload never sends its
  body at all.
- Matches every existing object-store API, so users' intuitions about error
  precedence hold.

**Cons**

- A developer with a broken signature and a malformed request sees only the
  signature error first, then the validation error once the signature is
  fixed. Two round trips to learn two faults.

### Option B: Validate first, then authenticate (predecessor order)

Run full parameter and body-shape validation, then verify the signature.

**Pros**

- One round trip reports the more "helpful" error to a developer whose request
  is malformed.

**Cons**

- Anonymous callers can probe parameter ranges and, if validation touches the
  store, resource existence.
- Body-dependent validation reads bodies from anyone.
- It was never chosen on merits. It was framework hook order.

### Option C: Interleave by cost

Run the cheapest checks first regardless of category. This is Option A stated
sloppily: authentication is already among the cheapest checks, and the only
things cheaper are the syntactic parses that authentication itself needs.
Named here so it is visibly the same decision, not a third one.

## Decision

**Option A, as a fixed ladder.** Processing proceeds through these stages in
order. The first stage that fails produces the response; later stages do not
run. Each stage maps to one family in the error table.

| Stage | What is checked | Needs the body? | Failure family |
|-------|-----------------|-----------------|----------------|
| 1. Syntax | Request line parses; path and query percent-decode; the route exists; headers the signature will read are well-formed | No | Malformed request, or unknown route |
| 2. Authentication | Key id known and enabled; timestamp within skew or presign not expired; listed signed headers present; signature matches | No | Unauthenticated (with the distinct clock, unknown-key, bad-signature, missing-header categories from ADR-0003) |
| 3. Authorization | The key may perform this method on this bucket and key (rules from the authorization ADR) | No | Forbidden, or not-found where the authorization model says existence is hidden |
| 4. Semantic validation | Bucket and key naming rules; parameter ranges and combinations; header values; declared sizes against limits; upload-session state that does not require the body | No | Invalid request |
| 5. Body | Read the body; enforce the size rule from ADR-0001 as bytes arrive; verify a signed content checksum at the end | Yes | Invalid request, or checksum mismatch, reported after or during the read |
| 6. State | Existence and conflicts: object not found, bucket not empty, session already completed | No, but runs after the body when the body exists | Not found, or conflict |

Rules that follow from the ladder:

- **The body is not touched before stage 5.** Handlers that need the body
  declare it, and nothing before stage 5 reads it. SDKs send
  `Expect: 100-continue` on every request with a body, so a rejection at
  stages 1 through 4 costs no upload bandwidth. Whether Go's HTTP server
  honors this the way the SDKs will rely on is the first probe listed below.
- **Stage 1 fails without disclosure.** An unroutable path is not found;
  a malformed request is malformed. Neither says anything the spec does not.
  Stage 1 never consults the store.
- **Anonymous routes are explicit.** Health, version, and reads on public
  buckets skip stage 2 by declaration on the route, never by absence of a
  signature. A request that carries a signature to an anonymous route is
  still verified, and fails if the signature is bad.
- **Stage 6 runs after the body.** An upload part for a completed session
  should fail at stage 4 (session state is known before the body) but an
  object PUT to a bucket deleted mid-flight fails at stage 6. Existence checks
  that need no body are stage 4 when they are about the request's own
  precondition and stage 6 when they are about the outcome.
- **Compound faults are conformance cases.** For each adjacent pair of stages,
  at least one case carries a fault in both and expects the earlier stage's
  answer. A ladder nobody tests is a ladder that drifts.

## Consequences

Easier:

- One `net/http` pipeline with six named stages. The package layout
  (open question 5) gets a concrete shape for the HTTP adapter.
- Error codes partition cleanly by stage. The error table can be organized by
  this ladder.
- Anonymous traffic is bounded to one HMAC of cost.

Harder:

- Developers see one fault per round trip. The error responses have to be
  good enough that this is a nuisance, not a mystery.
- The order is a contract. Moving a check between stages is a spec change
  with a conformance case to update, not a refactor.
- "Which stage is this check?" becomes a question to answer for every new
  check. The rule of thumb: if it can run without the body and without
  knowing the caller, it is stage 1; if it needs the caller, stage 3 or 4;
  if it needs the body, stage 5; if it is about the world after the request,
  stage 6.

## Probes this record asks for

1. **`Expect: 100-continue` in Go's server.** Send a signed-badly PUT with a
   large body and the expectation header; confirm the server answers with the
   authentication error before any body bytes are sent, and record whether the
   connection is reused afterwards or closed. The answer goes in the project
   file as the first Go-specific lesson.
2. **Raw path preservation.** Confirm the canonical path used at stage 2 is
   built from the request's escaped path as sent, not from a decoded and
   re-normalized form, for keys containing `%2F`, `+`, and unicode. Go's URL
   type keeps both forms; which one the handler sees is a known gotcha.

## Action items

1. [ ] Maintainer accepts, amends, or rejects this record.
2. [ ] Organize the error table in the spec by stage.
3. [ ] Add compound-fault conformance cases, one per adjacent stage pair.
4. [ ] Run the two probes above before the first PUT handler exists.
5. [ ] Reflect the six stages in the HTTP adapter's structure when open
       question 5 is decided.
