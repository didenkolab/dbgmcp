package model

// AllLaunchModes is every launch mode, in one place.
//
// Go's compiler will not check a switch for exhaustiveness, and two real
// defects came from that: conditions written as "not exec" silently swallowed
// attach when it arrived, so the server asked the debugger to build a binary
// for a process that was already running, and reported optimisations as
// disabled when nothing had been compiled.
//
// Anything that behaves differently per mode is expected to be covered by a
// test that walks this list, which is the nearest thing to exhaustiveness
// available here.
func AllLaunchModes() []LaunchMode {
	return []LaunchMode{LaunchTest, LaunchDebug, LaunchExec, LaunchAttach}
}

// AllStepKinds is every step kind, for the same reason.
func AllStepKinds() []StepKind {
	return []StepKind{StepOver, StepInto, StepOut}
}

// AllWatchModes is every watch mode, for the same reason.
func AllWatchModes() []WatchMode {
	return []WatchMode{WatchWrite, WatchRead, WatchReadWrite}
}
