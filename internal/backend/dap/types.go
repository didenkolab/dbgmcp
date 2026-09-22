package dap

// The subset of DAP's wire types this backend uses. They are declared here
// rather than pulled from a library so that what the server depends on is
// visible in one place and a protocol change shows up as a compile error.

type initializeResponse struct {
	SupportsConfigurationDoneRequest  bool `json:"supportsConfigurationDoneRequest"`
	SupportsFunctionBreakpoints       bool `json:"supportsFunctionBreakpoints"`
	SupportsConditionalBreakpoints    bool `json:"supportsConditionalBreakpoints"`
	SupportsHitConditionalBreakpoints bool `json:"supportsHitConditionalBreakpoints"`
	SupportsSetVariable               bool `json:"supportsSetVariable"`
	SupportsDataBreakpoints           bool `json:"supportsDataBreakpoints"`
	SupportsLogPoints                 bool `json:"supportsLogPoints"`
	SupportsTerminateRequest          bool `json:"supportsTerminateRequest"`
	SupportsEvaluateForHovers         bool `json:"supportsEvaluateForHovers"`
	SupportsStepInTargetsRequest      bool `json:"supportsStepInTargetsRequest"`
	SupportsExceptionInfoRequest      bool `json:"supportsExceptionInfoRequest"`
	SupportsDelayedStackTraceLoading  bool `json:"supportsDelayedStackTraceLoading"`
	SupportsClipboardContext          bool `json:"supportsClipboardContext"`
}

type source struct {
	Name string `json:"name,omitempty"`
	Path string `json:"path,omitempty"`
}

type sourceBreakpoint struct {
	Line         int    `json:"line"`
	Condition    string `json:"condition,omitempty"`
	HitCondition string `json:"hitCondition,omitempty"`
	// LogMessage turns a breakpoint into one that reports and carries on. It is
	// how this backend traces without the agent in the loop.
	LogMessage string `json:"logMessage,omitempty"`
}

type breakpointResult struct {
	ID       int    `json:"id"`
	Verified bool   `json:"verified"`
	Line     int    `json:"line"`
	Message  string `json:"message,omitempty"`
	Source   source `json:"source"`
}

type setBreakpointsResponse struct {
	Breakpoints []breakpointResult `json:"breakpoints"`
}

type thread struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

type threadsResponse struct {
	Threads []thread `json:"threads"`
}

type stackFrame struct {
	ID     int    `json:"id"`
	Name   string `json:"name"`
	Source source `json:"source"`
	Line   int    `json:"line"`
	Column int    `json:"column"`
}

type stackTraceResponse struct {
	StackFrames []stackFrame `json:"stackFrames"`
	TotalFrames int          `json:"totalFrames"`
}

type scope struct {
	Name               string `json:"name"`
	VariablesReference int    `json:"variablesReference"`
	Expensive          bool   `json:"expensive"`
}

type scopesResponse struct {
	Scopes []scope `json:"scopes"`
}

type variable struct {
	Name               string `json:"name"`
	Value              string `json:"value"`
	Type               string `json:"type,omitempty"`
	VariablesReference int    `json:"variablesReference"`
	// NamedVariables and IndexedVariables let a client tell a struct from a
	// list without fetching either.
	NamedVariables   int `json:"namedVariables,omitempty"`
	IndexedVariables int `json:"indexedVariables,omitempty"`
}

type variablesResponse struct {
	Variables []variable `json:"variables"`
}

type evaluateResponse struct {
	Result             string `json:"result"`
	Type               string `json:"type,omitempty"`
	VariablesReference int    `json:"variablesReference"`
}
