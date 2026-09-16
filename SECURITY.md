# Security

tunna is pre-release software. It has had no external security review.
Do not put it in front of data you cannot afford to lose or expose until a
release says otherwise.

## Reporting

Please report vulnerabilities privately through GitHub's
[private vulnerability reporting](https://github.com/tunnaio/tunna/security/advisories/new)
for this repository, not as a public issue. Include what you found, how to
reproduce it, and what you think the impact is. You will get an
acknowledgement, and a fix or an explanation, as soon as the maintainer can
manage; there is no team behind this and no guaranteed response time.

## What is in scope

- The server: authentication and authorization (ADR-0003, ADR-0008), the
  request ladder (ADR-0004), CORS (ADR-0009), anything that lets a request
  read or change what its key may not.
- The SDKs: anything that leaks a secret or a presigned URL, or signs
  something other than what it sends.
- The specification: a rule that is unsafe as written.

## What the design already says

The security model is written down, and reading it first tells you what is
a bug and what is a decision:

- Secrets are stored in usable form; file permissions on the data directory
  are the boundary (ADR-0003).
- A presigned URL is a credential and is never logged (ADR-0003).
- An admin may disable or delete its own key or the last admin key; the
  bootstrap variable is the recovery (ADR-0008).
- CORS is not a security boundary here, since nothing is ambient (ADR-0009).
