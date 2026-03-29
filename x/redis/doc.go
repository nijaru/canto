// Package redis provides a Redis-backed Coordinator for distributed
// per-session lane coordination across multiple processes.
//
// RedisCoordinator implements [runtime.Coordinator] using sorted sets for
// per-session FIFO queues and Redis hashes for lease state with TTL-based
// expiry. All critical operations (grant, renew, ack, nack) use Lua scripts
// for atomicity.
//
// Usage:
//
//	coord := redis.NewRedisCoordinator(client)
//	defer coord.Stop()
//
//	runner := runtime.NewRunner(store, agent, runtime.WithCoordinator(coord))
package redis
