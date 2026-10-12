//go:build windows

package agentworksclient

import (
	"context"
	"errors"
	"os"
)

func openMuseMonitorPrivate(string, bool) (*os.File, error) {
	return nil, errors.New("Muse Monitor requires Unix")
}
func createMuseMonitorFiles(string, MuseMonitorSubscription) error {
	return errors.New("Muse Monitor requires Unix")
}
func removeMuseMonitorFiles(string)  {}
func cleanupMuseMonitorFiles(string) {}
func openMuseMonitorWriter(context.Context, string) (*os.File, error) {
	return nil, errors.New("Muse Monitor requires Unix")
}
func waitMuseMonitorReader(string, context.CancelFunc) {}
func ReadMuseMonitorPipe(context.Context, string, string, string, func(MuseWatchHint) error) error {
	return errors.New("Muse Monitor requires Unix")
}

func holdMuseMonitorBroker(string) (*os.File, error) {
	return nil, errors.New("Muse Monitor requires Unix")
}
