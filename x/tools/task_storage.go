package tools

import (
	"github.com/go-json-experiment/json"
	"github.com/go-json-experiment/json/jsontext"
)

func (t *TaskTool) readTask(filename string) (*taskRecord, error) {
	b, err := t.root.ReadFile(filename)
	if err != nil {
		return nil, err
	}
	var rec taskRecord
	if err := json.Unmarshal(b, &rec); err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, &rec.raw); err != nil {
		return nil, err
	}
	return &rec, nil
}

func (t *TaskTool) writeTask(filename string, rec *taskRecord) error {
	fields, err := taskRecordFields(rec)
	if err != nil {
		return err
	}
	b, err := json.Marshal(fields, jsontext.WithIndent("  "))
	if err != nil {
		return err
	}
	return t.root.WriteFile(filename, append(b, '\n'), 0o644)
}

func taskRecordFields(rec *taskRecord) (map[string]jsontext.Value, error) {
	b, err := json.Marshal(rec)
	if err != nil {
		return nil, err
	}
	var fields map[string]jsontext.Value
	if err := json.Unmarshal(b, &fields); err != nil {
		return nil, err
	}
	merged := cloneTaskFields(rec.raw)
	if merged == nil {
		merged = map[string]jsontext.Value{}
	}
	for key, value := range fields {
		merged[key] = value
	}
	return merged, nil
}

func cloneTaskFields(src map[string]jsontext.Value) map[string]jsontext.Value {
	if len(src) == 0 {
		return nil
	}
	dst := make(map[string]jsontext.Value, len(src))
	for key, value := range src {
		dst[key] = append(jsontext.Value(nil), value...)
	}
	return dst
}
