// Package tunna is the core of the tunna object storage server: the domain
// types, the naming and size rules, the error kinds, and the interfaces its
// storage adapters implement. It performs no I/O itself (ADR-0005).
//
// Importers of this module usually want one of two things. Package sig is
// the request signer, canonical request, header and presigned forms, for
// building a client; it imports nothing from this package. This package
// holds the shared types and rules, for building an alternative server or
// adapter. The server itself is cmd/tunna, and internal/ is not importable.
//
// The wire contract this module implements is specified as data in the
// repository's spec/ directory; SpecVersion says which version.
package tunna
