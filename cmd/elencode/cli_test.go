package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestRunCLIStartsTheSessionWithoutArguments(t *testing.T) {
	handled, err := runCLI(nil, &bytes.Buffer{})
	if handled || err != nil {
		t.Errorf("runCLI(nil) = %v, %v, want it left to the session", handled, err)
	}
}

func TestRunCLIRunsTheNamedCommandWithTheRestOfTheArguments(t *testing.T) {
	var out bytes.Buffer
	handled, err := runCLI([]string{"version"}, &out)
	if !handled || err != nil {
		t.Fatalf("runCLI(version) = %v, %v", handled, err)
	}
	if !strings.HasPrefix(out.String(), "elencode ") {
		t.Errorf("version printed %q", out.String())
	}
}
