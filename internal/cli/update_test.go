package cli

import (
	"testing"
)

func TestUpdateDevVersion(t *testing.T) {
	code := updateCommand([]string{"--check"}, "dev")
	if code != 0 {
		t.Errorf("exit code = %d, want 0", code)
	}
}

func TestUpdateCheckForUpdateDev(t *testing.T) {
	ch := checkForUpdate("dev")
	result := <-ch
	if result.Available {
		t.Error("dev version should not report available updates")
	}
}

func TestPrintUpdateNoticeDoesNotPanic(t *testing.T) {
	printUpdateNotice(checkForUpdateResult{Available: true, Current: "1.0.0", Latest: "2.0.0"})
	printUpdateNotice(checkForUpdateResult{Err: nil}) // network error
	printUpdateNotice(checkForUpdateResult{Available: false})
}

func TestLastUpdateCheckFile(t *testing.T) {
	path := lastUpdateCheckFile()
	if path == "" {
		t.Error("lastUpdateCheckFile() should return a non-empty path")
	}
}
