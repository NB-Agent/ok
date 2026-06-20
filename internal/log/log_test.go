package log

import "testing"

func TestInfo_NoPanic(t *testing.T) {
	// All log functions must not panic with nil args.
	Info("test info")
	Info("test info with key", "value")
}

func TestWarn_NoPanic(t *testing.T) {
	Warn("test warn")
	Warn("test warn with key", "value")
}

func TestError_NoPanic(t *testing.T) {
	Error("test error")
	Error("test error with key", "value")
}

func TestDebug_NoPanic(t *testing.T) {
	Debug("test debug")
	Debug("test debug with key", "value")
}
