package restic

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The test binary doubles as a password command and as a fake restic,
// selected by OUROBOROS_TEST_HELPER. Variables without the RESTIC_ prefix
// pass through the runner's environment filter.
const (
	helperEnv     = "OUROBOROS_TEST_HELPER"
	fakeModeEnv   = "OUROBOROS_TEST_FAKE"
	testPassword  = "integration-test-password"
	wrongPassword = "not-the-password"
)

func TestMain(m *testing.M) {
	switch os.Getenv(helperEnv) {
	case "password":
		fmt.Print(testPassword)
		os.Exit(0)
	case "wrong-password":
		fmt.Print(wrongPassword)
		os.Exit(0)
	case "fake-restic":
		os.Exit(fakeRestic(os.Getenv(fakeModeEnv)))
	}
	os.Exit(m.Run())
}

// fakeRestic implements a few behaviours the runner must handle. The mode is
// "<name>" or "<name>:<arg>".
func fakeRestic(mode string) int {
	name, arg, _ := strings.Cut(mode, ":")
	switch name {
	case "args":
		json.NewEncoder(os.Stdout).Encode(argsAfterDashDash())
	case "env":
		fmt.Print(os.Getenv("RESTIC_PASSWORD") + "|" + os.Getenv("RESTIC_REPOSITORY"))
	case "lines":
		fmt.Print("first\r\nsecond\n\nthird")
	case "exit":
		code, _ := strconv.Atoi(arg)
		fmt.Fprintf(os.Stderr, `{"message_type":"exit_error","code":%d,"message":"Fatal: simulated"}`+"\n", code)
		return code
	case "plain-fail":
		fmt.Fprintln(os.Stderr, "some context")
		fmt.Fprintln(os.Stderr, "Fatal: plain text failure")
		return 1
	case "sleep":
		time.Sleep(time.Minute)
	case "flood":
		// Much more stderr than the runner keeps.
		line := strings.Repeat("x", 1023) + "\n"
		for range 1024 {
			os.Stderr.WriteString(line)
		}
		fmt.Fprintln(os.Stderr, "LAST")
		return 1
	case "priority":
		fmt.Print(priorityClass())
	default:
		fmt.Fprintln(os.Stderr, "unknown fake mode", mode)
		return 99
	}
	return 0
}

// argsAfterDashDash returns the arguments the runner passed, skipping the
// test framework's own.
func argsAfterDashDash() []string {
	for i, a := range os.Args {
		if a == "--fake--" {
			return os.Args[i+1:]
		}
	}
	return os.Args[1:]
}

// fakeRunner returns a runner whose "restic" is this test binary in mode.
func fakeRunner(t *testing.T, mode string) *runner {
	t.Helper()
	t.Setenv(helperEnv, "fake-restic")
	t.Setenv(fakeModeEnv, mode)
	return &runner{exe: os.Args[0], global: []string{"--fake--", "--repo", "R"}}
}
