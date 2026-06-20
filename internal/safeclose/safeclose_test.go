package safeclose

import (
	"testing"
)

type testCloser struct{}

func (testCloser) Close() error { return nil }

type testSimpleCloser struct{}

func (testSimpleCloser) Close() {}

func TestBaseline_LogNilNotPanic(t *testing.T) {
	t.Parallel()
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("Log with nil closer panicked: %v", r)
		}
	}()
	Log("test-nil", testCloser{})
}

func TestBaseline_LogNormalNotPanic(t *testing.T) {
	t.Parallel()
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("Log with ok closer panicked: %v", r)
		}
	}()
	Log("test-ok", testCloser{})
}

func TestBaseline_LogSimpleNilNotPanic(t *testing.T) {
	t.Parallel()
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("LogSimple with nil closer panicked: %v", r)
		}
	}()
	LogSimple("test-simple", testSimpleCloser{})
}

func TestBaseline_LogSimpleNormalNotPanic(t *testing.T) {
	t.Parallel()
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("LogSimple with ok closer panicked: %v", r)
		}
	}()
	LogSimple("test-simple", testSimpleCloser{})
}
