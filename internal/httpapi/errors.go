package httpapi

import "net/http"

// code is a wire error code. The list below is a transcription of
// spec/errors.json; error_test.go fails if the two ever differ.
type code string

const (
	// stage 1: syntax
	codeMalformedRequest code = "malformed_request"
	codeUnknownRoute     code = "unknown_route"
	codeMethodNotAllowed code = "method_not_allowed"

	// stage 2: authentication
	codeUnauthenticated     code = "unauthenticated"
	codeUnknownKey          code = "unknown_key"
	codeBadSignature        code = "bad_signature"
	codeMissingSignedHeader code = "missing_signed_header"
	codeClockSkew           code = "clock_skew"
	codePresignExpired      code = "presign_expired"
	codePresignTooLong      code = "presign_too_long"
	codePresignNotAllowed   code = "presign_not_allowed"

	// stage 3: authorization
	codeForbidden code = "forbidden"

	// stage 4: validation
	codeInvalidBucketName code = "invalid_bucket_name"
	codeInvalidKey        code = "invalid_key"
	codeInvalidParameter  code = "invalid_parameter"
	codeInvalidPartSize   code = "invalid_part_size"
	codeInvalidPartNumber code = "invalid_part_number"
	codeSessionNotActive  code = "session_not_active"

	// stage 5: body
	codeBodyTooLarge       code = "body_too_large"
	codeBodyLengthMismatch code = "body_length_mismatch"
	codeChecksumMismatch   code = "checksum_mismatch"

	// stage 6: state
	codeBucketNotFound   code = "bucket_not_found"
	codeObjectNotFound   code = "object_not_found"
	codeUploadNotFound   code = "upload_not_found"
	codeKeyNotFound      code = "key_not_found"
	codeBucketExists     code = "bucket_exists"
	codeBucketNotEmpty   code = "bucket_not_empty"
	codeUploadIncomplete code = "upload_incomplete"
	codePartSizeMismatch code = "part_size_mismatch"
	codeInternal         code = "internal"
	codeUnavailable      code = "unavailable"
)

// statusOf maps every code to its HTTP status, from spec/errors.json. A
// literal, never mutated. writeError derives the status from here so no call
// site can pair a code with the wrong status.
var statusOf = map[code]int{
	// stage 1: syntax
	codeMalformedRequest: http.StatusBadRequest,
	codeUnknownRoute:     http.StatusNotFound,
	codeMethodNotAllowed: http.StatusMethodNotAllowed,

	// stage 2: authentication
	codeUnauthenticated:     http.StatusUnauthorized,
	codeUnknownKey:          http.StatusUnauthorized,
	codeBadSignature:        http.StatusUnauthorized,
	codeMissingSignedHeader: http.StatusUnauthorized,
	codeClockSkew:           http.StatusUnauthorized,
	codePresignExpired:      http.StatusUnauthorized,
	codePresignTooLong:      http.StatusUnauthorized,
	codePresignNotAllowed:   http.StatusUnauthorized,

	// stage 3: authorization
	codeForbidden: http.StatusForbidden,

	// stage 4: validation
	codeInvalidBucketName: http.StatusUnprocessableEntity,
	codeInvalidKey:        http.StatusUnprocessableEntity,
	codeInvalidParameter:  http.StatusUnprocessableEntity,
	codeInvalidPartSize:   http.StatusUnprocessableEntity,
	codeInvalidPartNumber: http.StatusUnprocessableEntity,
	codeSessionNotActive:  http.StatusUnprocessableEntity,

	// stage 5: body
	codeBodyTooLarge:       http.StatusRequestEntityTooLarge,
	codeBodyLengthMismatch: http.StatusUnprocessableEntity,
	codeChecksumMismatch:   http.StatusUnprocessableEntity,

	// stage 6: state
	codeBucketNotFound:   http.StatusNotFound,
	codeObjectNotFound:   http.StatusNotFound,
	codeUploadNotFound:   http.StatusNotFound,
	codeKeyNotFound:      http.StatusNotFound,
	codeBucketExists:     http.StatusConflict,
	codeBucketNotEmpty:   http.StatusConflict,
	codeUploadIncomplete: http.StatusConflict,
	codePartSizeMismatch: http.StatusConflict,
	codeInternal:         http.StatusInternalServerError,
	codeUnavailable:      http.StatusServiceUnavailable,
}

// errorDetail is the inner object of the wire error body (spec/wire.md 9).
type errorDetail struct {
	Code    code           `json:"code"`
	Message string         `json:"message"`
	Details map[string]any `json:"details,omitempty"`
}

// errorBody is the wire error body: {"error": {...}}.
type errorBody struct {
	Error errorDetail `json:"error"`
}
