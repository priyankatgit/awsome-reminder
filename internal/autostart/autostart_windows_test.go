package autostart

import (
	"testing"

	"golang.org/x/sys/windows/registry"
)

func runtimeIsUnix() bool { return false }

func setupTest(t *testing.T) {
	old := runKeyPath
	runKeyPath = `Software\AwesomeReminderTest\Run`
	t.Cleanup(func() {
		runKeyPath = old
		_ = registry.DeleteKey(registry.CURRENT_USER, `Software\AwesomeReminderTest\Run`)
		_ = registry.DeleteKey(registry.CURRENT_USER, `Software\AwesomeReminderTest`)
	})
}

func readEntry(t *testing.T) string {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKeyPath, registry.QUERY_VALUE)
	if err != nil {
		t.Fatal(err)
	}
	defer k.Close()
	v, _, err := k.GetStringValue(valueName)
	if err != nil {
		t.Fatal(err)
	}
	return v
}
