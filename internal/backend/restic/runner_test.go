package restic

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestRunnerPassesArguments(t *testing.T) {
	r := fakeRunner(t, "args")
	out, err := r.run(context.Background(), call{args: []string{"snapshots", "--json"}})
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	if err := json.Unmarshal(out.stdout, &got); err != nil {
		t.Fatal(err)
	}
	want := []string{"--repo", "R", "snapshots", "--json"}
	if !slices.Equal(got, want) {
		t.Fatalf("args = %q, want %q", got, want)
	}
}

func TestRunnerStripsResticEnvironment(t *testing.T) {
	t.Setenv("RESTIC_PASSWORD", "leaked")
	t.Setenv("restic_repository", "elsewhere")
	r := fakeRunner(t, "env")
	out, err := r.run(context.Background(), call{args: []string{"x"}})
	if err != nil {
		t.Fatal(err)
	}
	if string(out.stdout) != "|" {
		t.Fatalf("restic saw %q", out.stdout)
	}
}

func TestRunnerStreamsLines(t *testing.T) {
	r := fakeRunner(t, "lines")
	var lines []string
	_, err := r.run(context.Background(), call{
		args:   []string{"x"},
		onLine: func(l []byte) error { lines = append(lines, string(l)); return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"first", "second", "third"}; !slices.Equal(lines, want) {
		t.Fatalf("lines = %q, want %q", lines, want)
	}
}

func TestRunnerLineCallbackError(t *testing.T) {
	r := fakeRunner(t, "lines")
	boom := errors.New("bad line")
	_, err := r.run(context.Background(), call{
		args:   []string{"x"},
		onLine: func([]byte) error { return boom },
	})
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want %v", err, boom)
	}
}

func TestRunnerExitCodes(t *testing.T) {
	tests := []struct {
		code int
		want error
	}{
		{3, ErrIncomplete},
		{10, ErrRepoNotFound},
		{11, ErrRepoLocked},
		{12, ErrWrongPassword},
		{130, ErrInterrupted},
	}
	others := []error{ErrIncomplete, ErrRepoNotFound, ErrRepoLocked, ErrWrongPassword, ErrInterrupted}
	for _, tt := range tests {
		r := fakeRunner(t, "exit:"+strconv.Itoa(tt.code))
		_, err := r.run(context.Background(), call{args: []string{"backup"}})
		var ee *ExitError
		if !errors.As(err, &ee) || ee.Code != tt.code || ee.Command != "backup" || ee.Message != "Fatal: simulated" {
			t.Fatalf("code %d: err = %#v", tt.code, err)
		}
		for _, o := range others {
			if errors.Is(err, o) != (o == tt.want) {
				t.Errorf("code %d: errors.Is(%v) = %v", tt.code, o, !(o == tt.want))
			}
		}
	}
}

func TestRunnerPlainTextFailure(t *testing.T) {
	r := fakeRunner(t, "plain-fail")
	_, err := r.run(context.Background(), call{args: []string{"check"}})
	var ee *ExitError
	if !errors.As(err, &ee) || ee.Code != 1 || !strings.Contains(ee.Message, "Fatal: plain text failure") {
		t.Fatalf("err = %v", err)
	}
}

func TestRunnerStderrIsBounded(t *testing.T) {
	r := fakeRunner(t, "flood")
	out, err := r.run(context.Background(), call{args: []string{"x"}})
	if err == nil {
		t.Fatal("want error")
	}
	if len(out.stderr) > stderrLimit || !strings.Contains(err.Error(), "LAST") {
		t.Fatalf("stderr %d bytes, err = %.80v", len(out.stderr), err)
	}
}

func TestRunnerTimeout(t *testing.T) {
	r := fakeRunner(t, "sleep")
	start := time.Now()
	_, err := r.run(context.Background(), call{args: []string{"prune"}, timeout: 500 * time.Millisecond})
	if !errors.Is(err, ErrTimeout) {
		t.Fatalf("err = %v, want ErrTimeout", err)
	}
	if time.Since(start) > 20*time.Second {
		t.Fatal("process was not stopped promptly")
	}
}

func TestRunnerCancel(t *testing.T) {
	r := fakeRunner(t, "sleep")
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(300*time.Millisecond, cancel)
	_, err := r.run(ctx, call{args: []string{"backup"}, timeout: time.Hour})
	if !errors.Is(err, context.Canceled) || errors.Is(err, ErrTimeout) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

func TestRunnerMissingExecutable(t *testing.T) {
	r := &runner{exe: t.TempDir() + `\missing.exe`}
	if _, err := r.run(context.Background(), call{args: []string{"x"}}); err == nil {
		t.Fatal("want error")
	}
}

func TestTailBuffer(t *testing.T) {
	tb := &tailBuffer{limit: 5}
	tb.Write([]byte("abc"))
	tb.Write([]byte("defg"))
	if got := string(tb.Bytes()); got != "cdefg" {
		t.Fatalf("got %q", got)
	}
}

func TestResticEnv(t *testing.T) {
	got := resticEnv([]string{"PATH=x", "RESTIC_PASSWORD=p", "Restic_Repository=r", "RESTICX=1", "HOME=h"})
	want := []string{"PATH=x", "RESTICX=1", "HOME=h"}
	if !slices.Equal(got, want) {
		t.Fatalf("got %q, want %q", got, want)
	}
}
