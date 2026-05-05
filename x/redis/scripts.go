package redis

import "github.com/redis/go-redis/v9"

// All scripts operate on per-session keys and use Redis atomicity guarantees.
//
// Keys per session:
//   - {prefix}{sid}:queue   — sorted set (score=seq, member=request_id)
//   - {prefix}{sid}:lease   — hash (active lease fields)
//   - {prefix}{sid}:seq     — persistent monotonic sequence counter
//   - {prefix}{sid}:att     — persistent attempt counter (reset on ack)
//   - {prefix}{sid}:tok     — persistent token counter (reset on ack)

var enqueueScript = redis.NewScript(`
local queue_key = KEYS[1]
local seq_key   = KEYS[2]
local request_id = ARGV[1]

-- Atomically increment sequence
local seq = redis.call('INCR', seq_key)

-- Add to sorted set (NX: reject if member already exists)
local added = redis.call('ZADD', queue_key, 'NX', seq, request_id)
if added == 0 then
    return 0
end
return seq
`)

var grantScript = redis.NewScript(`
local queue_key = KEYS[1]
local lease_key = KEYS[2]
local att_key   = KEYS[3]
local tok_key   = KEYS[4]
local request_id = ARGV[1]
local now_ms = tonumber(ARGV[2])
local ttl_ms = tonumber(ARGV[3])

-- Check active lease
local lease_req = redis.call('HGET', lease_key, 'request_id')
if lease_req and lease_req ~= '' then
    local expires = tonumber(redis.call('HGET', lease_key, 'expires_at_ms'))
    if expires > now_ms then
        if lease_req == request_id then
            return {1,
                tonumber(redis.call('HGET', lease_key, 'attempt')),
                tonumber(redis.call('HGET', lease_key, 'lease_token')),
                tonumber(redis.call('HGET', lease_key, 'granted_at_ms')),
                expires}
        end
        return {0}
    end
    -- Expired: clear lease hash; att/tok persist for reclaim tracking
    redis.call('DEL', lease_key)
end

-- Must be first in queue
local front = redis.call('ZRANGE', queue_key, 0, 0)
if #front == 0 or front[1] ~= request_id then
    return {0}
end

-- Increment persistent counters
local attempt = redis.call('INCR', att_key)
local token = redis.call('INCR', tok_key)
local expires_at = now_ms + ttl_ms
redis.call('HSET', lease_key,
    'request_id', request_id,
    'attempt', attempt,
    'lease_token', token,
    'granted_at_ms', now_ms,
    'expires_at_ms', expires_at)
-- TTL on lease hash, att, and tok prevents key leaks after crashes
local safety_ttl = ttl_ms * 6
redis.call('PEXPIRE', lease_key, safety_ttl)
redis.call('PEXPIRE', att_key, safety_ttl)
redis.call('PEXPIRE', tok_key, safety_ttl)

return {1, attempt, token, now_ms, expires_at}
`)

var renewScript = redis.NewScript(`
local lease_key = KEYS[1]
local request_id = ARGV[1]
local token = tonumber(ARGV[2])
local now_ms = tonumber(ARGV[3])
local new_expiry = tonumber(ARGV[4])

local lease_req = redis.call('HGET', lease_key, 'request_id')
if not lease_req or lease_req ~= request_id then
    return {0, 0}
end

local stored_token = tonumber(redis.call('HGET', lease_key, 'lease_token'))
if stored_token ~= token then
    return {0, 0}
end

local expires = tonumber(redis.call('HGET', lease_key, 'expires_at_ms'))
if expires <= now_ms then
    return {0, 1}
end

local attempt = tonumber(redis.call('HGET', lease_key, 'attempt'))
redis.call('HSET', lease_key, 'expires_at_ms', new_expiry)
redis.call('PEXPIRE', lease_key, (new_expiry - now_ms) * 6)

return {1, attempt, token, now_ms, new_expiry}
`)

var ackScript = redis.NewScript(`
local queue_key = KEYS[1]
local lease_key = KEYS[2]
local att_key   = KEYS[3]
local tok_key   = KEYS[4]
local request_id = ARGV[1]
local token = tonumber(ARGV[2])

local lease_req = redis.call('HGET', lease_key, 'request_id')
if not lease_req or lease_req ~= request_id then
    return {0, 0}
end

local stored_token = tonumber(redis.call('HGET', lease_key, 'lease_token'))
if stored_token ~= token then
    return {0, 0}
end

-- Server-side expiry check
local t = redis.call('TIME')
local now_ms = tonumber(t[1]) * 1000 + math.floor(tonumber(t[2]) / 1000)
local expires = tonumber(redis.call('HGET', lease_key, 'expires_at_ms'))
if expires <= now_ms then
    return {0, 1}
end

redis.call('ZREM', queue_key, request_id)
redis.call('DEL', lease_key, att_key, tok_key)
return {1, 0}
`)

var nackScript = redis.NewScript(`
local lease_key = KEYS[1]
local request_id = ARGV[1]
local token = tonumber(ARGV[2])

local lease_req = redis.call('HGET', lease_key, 'request_id')
if not lease_req or lease_req ~= request_id then
    return {0, 0}
end

local stored_token = tonumber(redis.call('HGET', lease_key, 'lease_token'))
if stored_token ~= token then
    return {0, 0}
end

local t = redis.call('TIME')
local now_ms = tonumber(t[1]) * 1000 + math.floor(tonumber(t[2]) / 1000)
local expires = tonumber(redis.call('HGET', lease_key, 'expires_at_ms'))
if expires <= now_ms then
    return {0, 1}
end

redis.call('DEL', lease_key)
return {1, 0}
`)
