package main

import (
	"context"
	"os"
	"testing"
)

// mainHelperEnv makes the test binary behave as the ouroboros executable,
// so that restic can run it as its password command during tests.
const mainHelperEnv = "OUROBOROS_TEST_MAIN"

func TestMain(m *testing.M) {
	if os.Getenv(mainHelperEnv) == "1" {
		os.Exit(run(context.Background(), os.Args[1:], os.Stdout, os.Stderr))
	}
	os.Exit(m.Run())
}
