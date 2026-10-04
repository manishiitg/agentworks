package store

import (
	"encoding/json"
	"reflect"
	"strings"
	"unicode/utf8"
)

// AuditPayloadMaxBytes bounds each recorded input/output independently. Capture
// also bounds traversal and string copies before serialization, so a large MCP
// response cannot cause an unbounded audit allocation. SQLite writes stay in
// the existing audit worker; capturing a detached snapshot happens at admission.
const AuditPayloadMaxBytes = 64 << 10

func CaptureAuditPayload(value any) (json.RawMessage, bool) {
	c := payloadCapture{remaining: AuditPayloadMaxBytes / 2}
	data, err := json.Marshal(c.copy(reflect.ValueOf(value), 0))
	if err != nil {
		return json.RawMessage(`"[payload could not be encoded]"`), true
	}
	if len(data) > AuditPayloadMaxBytes {
		// Escaping can expand strings. Preserve valid JSON, never slice JSON bytes.
		data, _ = json.Marshal(clipUTF8(string(data), AuditPayloadMaxBytes/8) + "… [truncated]")
		c.truncated = true
	}
	return data, c.truncated
}

type payloadCapture struct {
	remaining int
	truncated bool
}

func clipUTF8(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

func (c *payloadCapture) text(s string) string {
	if len(s) > c.remaining {
		s = clipUTF8(s, max(0, c.remaining))
		c.truncated = true
		c.remaining = 0
		return s + "… [truncated]"
	}
	c.remaining -= len(s)
	return s
}

func (c *payloadCapture) copy(v reflect.Value, depth int) any {
	if !v.IsValid() {
		return nil
	}
	if c.remaining < 64 || depth > 32 {
		c.truncated = true
		return "[truncated]"
	}
	c.remaining -= 64 // Bounds node count, key overhead and cyclic structures.
	for v.Kind() == reflect.Interface || v.Kind() == reflect.Pointer {
		depth++
		if depth > 32 {
			c.truncated = true
			return "[truncated]"
		}
		if v.IsNil() {
			return nil
		}
		v = v.Elem()
	}
	if v.Type() == reflect.TypeOf(json.Number("")) {
		number := v.String()
		if len(number) > c.remaining {
			c.truncated = true
			c.remaining = 0
			return "[truncated]"
		}
		c.remaining -= len(number)
		return json.Number(number)
	}
	if v.Type() == reflect.TypeOf(json.RawMessage{}) {
		raw := v.Interface().(json.RawMessage)
		if len(raw) > c.remaining {
			c.truncated = true
			return "[truncated JSON payload]"
		}
		var decoded any
		if json.Unmarshal(raw, &decoded) != nil {
			c.truncated = true
			return "[invalid JSON payload]"
		}
		return c.copy(reflect.ValueOf(decoded), depth+1)
	}
	switch v.Kind() {
	case reflect.String:
		return c.text(v.String())
	case reflect.Bool:
		return v.Bool()
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return v.Int()
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return v.Uint()
	case reflect.Float32, reflect.Float64:
		return v.Float()
	case reflect.Map:
		if v.IsNil() {
			return nil
		}
		out := map[string]any{}
		iter := v.MapRange()
		for iter.Next() {
			if c.remaining < 64 {
				c.truncated = true
				break
			}
			if iter.Key().Kind() != reflect.String {
				c.truncated = true
				continue
			}
			key := c.text(iter.Key().String())
			out[key] = c.copy(iter.Value(), depth+1)
		}
		return out
	case reflect.Slice, reflect.Array:
		if v.Kind() == reflect.Slice && v.IsNil() {
			return nil
		}
		out := make([]any, 0, min(v.Len(), 512))
		for i := 0; i < v.Len(); i++ {
			if c.remaining < 64 {
				c.truncated = true
				break
			}
			out = append(out, c.copy(v.Index(i), depth+1))
		}
		return out
	case reflect.Struct:
		out := map[string]any{}
		for i := 0; i < v.NumField(); i++ {
			field, value := v.Type().Field(i), v.Field(i)
			if field.PkgPath != "" {
				continue
			}
			tag := strings.Split(field.Tag.Get("json"), ",")
			if tag[0] == "-" {
				continue
			}
			if len(tag) > 1 && tag[1] == "omitempty" && (value.IsZero() || ((value.Kind() == reflect.Map || value.Kind() == reflect.Slice || value.Kind() == reflect.String) && value.Len() == 0)) {
				continue
			}
			if c.remaining < 64 {
				c.truncated = true
				break
			}
			if field.Anonymous && tag[0] == "" {
				if embedded, ok := c.copy(value, depth+1).(map[string]any); ok {
					for k, val := range embedded {
						out[k] = val
					}
				}
				continue
			}
			name := tag[0]
			if name == "" {
				name = field.Name
			}
			out[c.text(name)] = c.copy(value, depth+1)
		}
		return out
	default:
		c.truncated = true
		return "[unsupported payload value]"
	}
}
