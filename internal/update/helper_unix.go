//go:build !windows

package update

import (
	"context"
	"errors"
)

func scheduleHelper(State, string) error {
	return errors.New("self-replacement helper is only used on Windows")
}
func scheduleRollbackHelper(string, string, string, string) error {
	return errors.New("rollback helper is only used on Windows")
}
func WaitForParent(context.Context, int) error { return nil }
func helperDescription(string) string          { return "update helper" }
