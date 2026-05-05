package redis

func (c *RedisCoordinator) queueKey(sessionID string) string {
	return c.keyPrefix + sessionID + ":queue"
}

func (c *RedisCoordinator) leaseKey(sessionID string) string {
	return c.keyPrefix + sessionID + ":lease"
}

func (c *RedisCoordinator) attKey(sessionID string) string {
	return c.keyPrefix + sessionID + ":att"
}

func (c *RedisCoordinator) tokKey(sessionID string) string {
	return c.keyPrefix + sessionID + ":tok"
}

func (c *RedisCoordinator) seqKey(sessionID string) string {
	return c.keyPrefix + sessionID + ":seq"
}
