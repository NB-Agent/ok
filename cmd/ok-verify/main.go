package main

import (
	"golang.org/x/tools/go/analysis/multichecker"

	"github.com/NB-Agent/ok/internal/verification"
)

func main() {
	multichecker.Main(
		verification.RecoverCheck,
		verification.CloseCheck,
		verification.RawAssert,
		verification.NilMapWrite,
		verification.SwitchDefault,
		verification.DoubleClose,
		verification.TmpDirCheck,
		verification.MutexCopy,
		verification.DeferInLoop,
		verification.LoopClosure,
		verification.SprintfHex,
		verification.SleepInLoop,
		verification.StringCastLoop,
		verification.ContextBg,
		verification.PreallocSlice,
		verification.LateCancel,
	)
}
