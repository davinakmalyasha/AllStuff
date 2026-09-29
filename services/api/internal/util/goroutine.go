package util

import (
	"log/slog"
	"runtime/debug"
)

// Go runs fn in a new goroutine with panic recovery.
//
// WHY THIS EXISTS
// ---------------
// `recover()` only works on the goroutine that deferred it. The HTTP layer has
// one (middleware.withRecover), so a panic while serving a request becomes a
// 500. Every OTHER goroutine had no recovery at all, and a panic in any of them
// is not a failed operation — it terminates the entire process. That applies to:
//
//	handlers_chat.go   push fan-out after a send
//	chat.go            link-preview fetch after a message
//	community.go       announcement delivery
//	notifications.go   per-recipient email dispatch (one goroutine PER recipient)
//
// With a follower count in the thousands, the notification fan-out alone means
// thousands of unrecovered goroutines per announcement, any of which can take
// the API down for all users. `jobs.Run` grew its own inline recover for the
// same reason; this makes the pattern available everywhere instead of relying on
// each call site to remember.
//
// A panic in one recipient's email must not abort the rest of the fan-out, so
// recovery here is the whole point rather than a nicety.
//
// The stack is logged: a bare "panic: ..." line is not enough to locate a
// failure inside a fan-out loop.
func Go(fn func()) {
	go func() {
		defer func() {
			if r := recover(); r != nil {
				slog.Error("panic in background goroutine",
					"panic", r,
					"stack", string(debug.Stack()))
			}
		}()
		fn()
	}()
}

// GoNamed is Go with a name attached to the log line, so a fan-out can be
// correlated with the operation that spawned it (e.g. "notification-email").
func GoNamed(name string, fn func()) {
	go func() {
		defer func() {
			if r := recover(); r != nil {
				slog.Error("panic in background goroutine",
					"task", name,
					"panic", r,
					"stack", string(debug.Stack()))
			}
		}()
		fn()
	}()
}
