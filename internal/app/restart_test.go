package app

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestWaitForParentStripsFlag(t *testing.T) {
	start := time.Now()
	got := WaitForParent([]string{"--after-setup", "999999", "--capture"})
	if !reflect.DeepEqual(got, []string{"--capture"}) {
		t.Fatalf("got %v", got)
	}
	if d := time.Since(start); d > time.Second {
		t.Fatalf("took %v", d)
	}
}

func TestWaitForParentNoFlag(t *testing.T) {
	in := []string{"--capture", "x"}
	if got := WaitForParent(in); !reflect.DeepEqual(got, in) {
		t.Fatalf("got %v", got)
	}
}

func TestRestartFlag(t *testing.T) {
	restartRequested.Store(false)
	if RestartRequested() {
		t.Fatal("set initially")
	}
	RequestRestart()
	t.Cleanup(func() { restartRequested.Store(false) })
	if !RestartRequested() {
		t.Fatal("not set")
	}
}

func TestRunRequiresDataDir(t *testing.T) {
	err := Run(Options{})
	if err == nil || !strings.Contains(err.Error(), "data folder") {
		t.Fatalf("err = %v", err)
	}
}
