package memory

func metadataMatchesFilter(metadata, filter map[string]any) bool {
	for key, want := range filter {
		got, ok := metadata[key]
		if !ok || !metadataValueEqual(got, want) {
			return false
		}
	}
	return true
}

func metadataValueEqual(got, want any) bool {
	if gotNumber, ok := numericMetadataValue(got); ok {
		wantNumber, ok := numericMetadataValue(want)
		return ok && gotNumber == wantNumber
	}

	switch want := want.(type) {
	case nil:
		return got == nil
	case string:
		got, ok := got.(string)
		return ok && got == want
	case bool:
		got, ok := got.(bool)
		return ok && got == want
	default:
		return false
	}
}

func numericMetadataValue(v any) (float64, bool) {
	switch v := v.(type) {
	case int:
		return float64(v), true
	case int8:
		return float64(v), true
	case int16:
		return float64(v), true
	case int32:
		return float64(v), true
	case int64:
		return float64(v), true
	case uint:
		return float64(v), true
	case uint8:
		return float64(v), true
	case uint16:
		return float64(v), true
	case uint32:
		return float64(v), true
	case uint64:
		return float64(v), true
	case float32:
		return float64(v), true
	case float64:
		return v, true
	default:
		return 0, false
	}
}
