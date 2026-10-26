package core

// Test-only views of unexported helpers for the core_test package.

// SensitiveHeader and SensitiveValue are the raw record's credential
// rules (raw.go), for FuzzRawRedaction's byte check.
var (
	SensitiveHeader = sensitiveHeader
	SensitiveValue  = sensitiveValue
)

// SourceRel is the manifest's machine-independent source path (U23.1-fix).
var SourceRel = sourceRel
