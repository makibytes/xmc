package cmd

import (
	"encoding/json"
	"reflect"
	"strings"

	"github.com/makibytes/xmc/broker/backends"
)

// recordForDisplay projects a message onto the same messageRecord schema used
// by --ndjson export/import (cmd/ndjson.go), so -J JSON output and the AI
// shell's metadata view share one schema instead of a second, divergence-
// prone definition. includePayload controls whether Data/DataBase64 appear
// (the AI shell's metadata view omits the payload; -J always includes it).
// includeInternalMetadata opts into broker-specific display fields (Kafka
// partition/offset, IBM MQ MQMD fields, ...), which never appear in NDJSON
// export since they aren't portable across brokers.
func recordForDisplay(msg *backends.Message, includePayload, includeInternalMetadata bool) messageRecord {
	rec := newMessageRecord(msg)
	if !includePayload {
		rec.Data = ""
		rec.DataBase64 = ""
	}
	if includeInternalMetadata {
		rec.InternalMetadata = pruneMap(msg.InternalMetadata)
	}
	return rec
}

// recordAsMap projects rec to a map[string]any via a JSON round-trip, so the
// key names and omitempty behavior always match the messageRecord JSON tags
// exactly — no separate field list to keep in sync. Used by the AI shell's
// metadata view, which renders the same data as either JSON or YAML.
func recordAsMap(rec messageRecord) (map[string]any, error) {
	data, err := json.Marshal(rec)
	if err != nil {
		return nil, err
	}
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func pruneMap(in map[string]any) map[string]any {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]any, len(in))
	for k, v := range in {
		if pruned, ok := pruneValue(v); ok {
			out[k] = pruned
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func pruneValue(v any) (any, bool) {
	if v == nil {
		return nil, false
	}

	switch t := v.(type) {
	case string:
		s := strings.TrimSpace(t)
		if s == "" || strings.EqualFold(s, "<nil>") {
			return nil, false
		}
		return t, true
	case map[string]any:
		m := pruneMap(t)
		return m, len(m) > 0
	case []any:
		items := make([]any, 0, len(t))
		for _, item := range t {
			if pruned, ok := pruneValue(item); ok {
				items = append(items, pruned)
			}
		}
		return items, len(items) > 0
	}

	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Pointer, reflect.Interface:
		if rv.IsNil() {
			return nil, false
		}
		return pruneValue(rv.Elem().Interface())
	case reflect.Map:
		if rv.Len() == 0 {
			return nil, false
		}
		out := map[string]any{}
		iter := rv.MapRange()
		for iter.Next() {
			if iter.Key().Kind() != reflect.String {
				continue
			}
			if pruned, ok := pruneValue(iter.Value().Interface()); ok {
				out[iter.Key().String()] = pruned
			}
		}
		if len(out) == 0 {
			return nil, false
		}
		return out, true
	case reflect.Slice, reflect.Array:
		if rv.Len() == 0 {
			return nil, false
		}
		items := make([]any, 0, rv.Len())
		for i := 0; i < rv.Len(); i++ {
			if pruned, ok := pruneValue(rv.Index(i).Interface()); ok {
				items = append(items, pruned)
			}
		}
		if len(items) == 0 {
			return nil, false
		}
		return items, true
	}

	return v, true
}
