// Package safego runs functions so that a panic in one is recovered, logged
// with its stack and counted, rather than crashing the process: one poison
// input must not take every process that reads it down with it.
package safego

import (
	"context"
	"fmt"
	"log/slog"
	"runtime/debug"
)

// PanicError is a recovered panic, returned by Call.
type PanicError struct {
	// Value is what the function panicked with.
	Value any
	// Stack is the goroutine's stack at the panic.
	Stack []byte
}

func (e *PanicError) Error() string { return fmt.Sprintf("panic: %v", e.Value) }

type hookKey struct{}

// WithPanicHook returns a context whose Go and Call report every recovered
// panic to hook, with the name the function was run under, such as to count
// it in a metric. Without a hook in the context a panic is logged and not
// counted, so code spawning goroutines takes its context from whoever
// installed the hook.
func WithPanicHook(ctx context.Context, hook func(name string)) context.Context {
	return context.WithValue(ctx, hookKey{}, hook)
}

// Call runs fn and returns its error; a panic in fn is recovered, logged at
// error with its stack, reported to the context's panic hook and returned as
// a *PanicError.
func Call(ctx context.Context, name string, logger *slog.Logger, fn func(context.Context) error) (err error) {
	defer func() {
		if v := recover(); v != nil {
			p := &PanicError{Value: v, Stack: debug.Stack()}
			if logger == nil {
				logger = slog.Default()
			}
			logger.ErrorContext(ctx, "recovered a panic", "name", name, "panic", fmt.Sprint(v), "stack", string(p.Stack))
			if hook, ok := ctx.Value(hookKey{}).(func(string)); ok {
				hook(name)
			}
			err = p
		}
	}()
	return fn(ctx)
}

// Go runs fn in a new goroutine under Call's recovery.
func Go(ctx context.Context, name string, logger *slog.Logger, fn func(context.Context)) {
	go func() {
		_ = Call(ctx, name, logger, func(ctx context.Context) error {
			fn(ctx)
			return nil
		})
	}()
}
