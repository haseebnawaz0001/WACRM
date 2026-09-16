package automation

import "log/slog"

// Logger is the subset of logging this package needs.
//
// It is an interface because the server logs through zerodha/logf while tests
// and any standalone use reach for the standard library — and neither is worth
// forcing on the other for three method calls.
type Logger interface {
	Info(msg string, fields ...any)
	Warn(msg string, fields ...any)
	Error(msg string, fields ...any)
}

// slogLogger adapts the standard library, which is the fallback when no logger
// is supplied.
type slogLogger struct{ l *slog.Logger }

func (s slogLogger) Info(msg string, fields ...any)  { s.l.Info(msg, fields...) }
func (s slogLogger) Warn(msg string, fields ...any)  { s.l.Warn(msg, fields...) }
func (s slogLogger) Error(msg string, fields ...any) { s.l.Error(msg, fields...) }
