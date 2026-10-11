package runtime

// BreakpointSource exposes the source behind Breakpoints to the
// external tests: they wait on it for a PUT breakpoints frame to land.
var BreakpointSource = breakpointSource
