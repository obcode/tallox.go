package zpa

import (
	"context"
	"time"
)

// SetSleepForTest replaces the backoff sleep so the retry tests finish in microseconds.
//
// In an export_test.go rather than as an exported field, because it is a seam for tests and
// not a knob: a sleep function on the public Config would be a way to configure a client that
// retries without waiting, which is not a thing anybody should be able to do by accident.
func SetSleepForTest(c *Client, sleep func(context.Context, time.Duration) error) {
	c.sleep = sleep
}
