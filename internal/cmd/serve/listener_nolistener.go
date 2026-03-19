//go:build notcp && nouds

package serve

// This file is only compiled when both TCP and Unix domain socket listeners
// are excluded. The undefined reference below produces a build error on purpose.
var _ = at_least_one_listener_type_must_be_enabled
