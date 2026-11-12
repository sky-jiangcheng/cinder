package version

// CoreVersion is the version of the memory-layer contract — the outward face
// that agents and IDE plugins program against: the MCP tool set, the HTTP
// /api/rpc surface and the SQLite schema.
//
// It moves on its own schedule, not with the release version. Version.Version
// answers "which build is this" (the desktop app ships UI changes every few
// days); CoreVersion answers "what may a client rely on" (the contract
// changes rarely). Conflating the two meant a client had to guess whether a
// version bump was cosmetic or breaking.
//
//	minor — additive: a new tool, a new optional field, a new endpoint
//	patch — no contract change at all
//	major — breaking: a tool removed or renamed, a field's meaning changed
//
// The value is pinned next to the tool list in cmd/mcp/main_test.go, and
// scripts/check-version-drift.mjs fails if the two drift apart.
const CoreVersion = "2.0.0"

// CoreVersionPrevious / CoreVersionChange make the bump itself checkable.
// The old gate only compared two copies of the number, so "deleted a tool but
// only bumped minor" passed. scripts/check-version-drift.mjs now validates
// that the declared change kind matches the actual distance between the two
// versions. ADR-0055 D1 is a major: failures moved from in-band text to
// IsError=true, which changes how a client decides a call failed.
const CoreVersionPrevious = "1.5.0"
const CoreVersionChange = "major" // major | minor | patch
