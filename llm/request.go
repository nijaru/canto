package llm

// Clone returns a deep copy of r's mutable request fields.
func (r *Request) Clone() *Request {
	if r == nil {
		return nil
	}
	clone := *r
	clone.Messages = cloneMessages(r.Messages)
	if len(r.Tools) > 0 {
		clone.Tools = make([]*Spec, len(r.Tools))
		for i, spec := range r.Tools {
			if spec == nil {
				continue
			}
			copied := *spec
			if spec.CacheControl != nil {
				cacheControl := *spec.CacheControl
				copied.CacheControl = &cacheControl
			}
			clone.Tools[i] = &copied
		}
	}
	return &clone
}

// InsertMessage inserts msg at index and keeps CachePrefixMessages aligned.
// If the insertion is inside the current cache prefix, the prefix grows with it.
func (r *Request) InsertMessage(index int, msg Message) {
	r.insertMessage(index, msg)
	if r.CachePrefixMessages > 0 && index < r.CachePrefixMessages {
		r.CachePrefixMessages++
	}
}

// InsertPrefixMessage inserts msg as part of the stable cache prefix.
func (r *Request) InsertPrefixMessage(index int, msg Message) {
	r.insertMessage(index, msg)
	if r.CachePrefixMessages > 0 && index <= r.CachePrefixMessages {
		r.CachePrefixMessages++
	}
}

// InsertAfterCachePrefix inserts msg immediately after the stable cache prefix.
func (r *Request) InsertAfterCachePrefix(msg Message) {
	index := r.CachePrefixMessages
	if index <= 0 || index > len(r.Messages) {
		index = 0
		for index < len(r.Messages) &&
			(r.Messages[index].Role == RoleSystem || r.Messages[index].Role == RoleDeveloper) {
			index++
		}
	}
	r.InsertMessage(index, msg)
}

// PrependMessage inserts msg at the start of the request. If the request already
// has a stable cache prefix, the new message becomes part of that prefix.
func (r *Request) PrependMessage(msg Message) {
	r.InsertPrefixMessage(0, msg)
}

// AppendMessage appends msg after the current request.
func (r *Request) AppendMessage(msg Message) {
	r.Messages = append(r.Messages, msg)
}

func (r *Request) insertMessage(index int, msg Message) {
	if index < 0 || index > len(r.Messages) {
		panic("llm.Request.InsertMessage: index out of range")
	}
	r.Messages = append(r.Messages, Message{})
	copy(r.Messages[index+1:], r.Messages[index:])
	r.Messages[index] = msg
}

func cloneMessages(messages []Message) []Message {
	if len(messages) == 0 {
		return nil
	}
	cloned := make([]Message, len(messages))
	for i, msg := range messages {
		cloned[i] = cloneMessage(msg)
	}
	return cloned
}

func cloneMessage(msg Message) Message {
	if len(msg.ThinkingBlocks) > 0 {
		msg.ThinkingBlocks = append([]ThinkingBlock(nil), msg.ThinkingBlocks...)
	}
	if len(msg.Calls) > 0 {
		msg.Calls = append([]Call(nil), msg.Calls...)
	}
	if msg.CacheControl != nil {
		cacheControl := *msg.CacheControl
		msg.CacheControl = &cacheControl
	}
	return msg
}
