//go:build windows

package restic

import (
	"context"
	"strconv"
	"testing"

	"golang.org/x/sys/windows"

	"github.com/WildPino/ouroboros/internal/config"
)

func priorityClass() string {
	c, err := windows.GetPriorityClass(windows.CurrentProcess())
	if err != nil {
		return "error: " + err.Error()
	}
	return strconv.FormatUint(uint64(c), 10)
}

func TestRunnerPriority(t *testing.T) {
	tests := []struct {
		priority string
		want     uint32
	}{
		{config.PriorityIdle, windows.IDLE_PRIORITY_CLASS},
		{config.PriorityBelowNormal, windows.BELOW_NORMAL_PRIORITY_CLASS},
		{config.PriorityNormal, windows.NORMAL_PRIORITY_CLASS},
	}
	for _, tt := range tests {
		r := fakeRunner(t, "priority")
		r.priority = tt.priority
		out, err := r.run(context.Background(), call{args: []string{"x"}})
		if err != nil {
			t.Fatal(err)
		}
		if got := string(out.stdout); got != strconv.FormatUint(uint64(tt.want), 10) {
			t.Errorf("%s: priority class %s, want %d", tt.priority, got, tt.want)
		}
	}
}
