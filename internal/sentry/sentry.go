package sentry

import (
	"fmt"
	"runtime"
	"time"

	"github.com/getsentry/sentry-go"

	"github.com/Blink-Build-Studios/little-tyke/cmd/little-tyke/cmd/version"
)

// Init initializes Sentry with the given DSN and environment.
func Init(dsn, environment string) (bool, error) {
	if dsn == "" {
		return false, nil
	}
	if environment == "" {
		environment = "development"
	}

	err := sentry.Init(sentry.ClientOptions{
		Dsn:              dsn,
		Release:          "little-tyke@" + version.Version,
		Environment:      environment,
		TracesSampleRate: 0.2,
	})
	if err != nil {
		return false, err
	}
	return true, nil
}

// Enabled returns true if Sentry has been initialized.
func Enabled() bool {
	return sentry.CurrentHub().Client() != nil
}

// Flush drains the Sentry event queue.
func Flush(timeout time.Duration) {
	sentry.Flush(timeout)
}

// Wrap attaches a stack trace to an existing error.
func Wrap(err error) error {
	if err == nil {
		return nil
	}
	return &stackError{err: err, frames: captureFrames(1)}
}

// Errorf creates a new error with a stack trace.
func Errorf(format string, args ...any) error {
	return &stackError{err: fmt.Errorf(format, args...), frames: captureFrames(1)}
}

type stackError struct {
	err    error
	frames []runtime.Frame
}

func (e *stackError) Error() string { return e.err.Error() }
func (e *stackError) Unwrap() error { return e.err }

func captureFrames(skip int) []runtime.Frame {
	pcs := make([]uintptr, 50)
	n := runtime.Callers(skip+2, pcs)
	pcs = pcs[:n]
	var frames []runtime.Frame
	iter := runtime.CallersFrames(pcs)
	for {
		frame, more := iter.Next()
		frames = append(frames, frame)
		if !more {
			break
		}
	}
	return frames
}
