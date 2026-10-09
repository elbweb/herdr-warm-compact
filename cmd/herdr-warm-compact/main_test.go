package main

import "testing"

func TestStopWithNothingRunningSucceeds(t *testing.T) {
	t.Setenv("HERDR_PLUGIN_CONFIG_DIR", t.TempDir())
	if err := dispatch([]string{"stop"}); err != nil {
		t.Fatal(err)
	}
}
