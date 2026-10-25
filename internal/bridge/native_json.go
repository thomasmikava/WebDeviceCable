package bridge

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strconv"
	"strings"
)

// A small, explicit JSON Schema subset. No references, remote resolution, or
// executable validators. Unsupported keywords fail at admission, before billing.
func checkOutputSchema(raw json.RawMessage) error {
	if len(raw) == 0 {
		return nil
	}
	if len(raw) > 8192 {
		return errors.New("outputSchema exceeds 8 KiB")
	}
	var schema map[string]any
	if json.Unmarshal(raw, &schema) != nil {
		return errors.New("outputSchema must be a JSON object")
	}
	if schema["type"] != "object" {
		return errors.New("outputSchema root must be an object")
	}
	return inspectSchema(schema, 0)
}
func inspectSchema(schema map[string]any, depth int) error {
	if depth > 12 {
		return errors.New("outputSchema exceeds 12 levels")
	}
	for key := range schema {
		switch key {
		case "type", "properties", "required", "additionalProperties", "items", "enum", "description", "title":
		default:
			return fmt.Errorf("unsupported outputSchema keyword: %s", key)
		}
	}
	typ, ok := schema["type"].(string)
	if !ok {
		return errors.New("each schema needs a single type")
	}
	for _, key := range []string{"title", "description"} {
		if v, exists := schema[key]; exists {
			if _, ok := v.(string); !ok {
				return fmt.Errorf("%s must be text", key)
			}
		}
	}
	if values, exists := schema["enum"]; exists {
		if list, ok := values.([]any); !ok || len(list) == 0 || len(list) > 64 {
			return errors.New("enum must contain 1–64 values")
		}
	}
	switch typ {
	case "object":
		properties, ok := schema["properties"].(map[string]any)
		if !ok || len(properties) > 64 {
			return errors.New("objects need at most 64 properties")
		}
		if schema["additionalProperties"] != false {
			return errors.New("objects require additionalProperties: false")
		}
		required, ok := schema["required"].([]any)
		if !ok || len(required) != len(properties) {
			return errors.New("every object property must be required")
		}
		seen := map[string]bool{}
		for _, key := range required {
			name, ok := key.(string)
			if !ok || seen[name] {
				return errors.New("invalid required properties")
			}
			if _, exists := properties[name]; !exists {
				return errors.New("unknown required property")
			}
			seen[name] = true
		}
		for _, value := range properties {
			child, ok := value.(map[string]any)
			if !ok {
				return errors.New("invalid property schema")
			}
			if e := inspectSchema(child, depth+1); e != nil {
				return e
			}
		}
	case "array":
		child, ok := schema["items"].(map[string]any)
		if !ok {
			return errors.New("arrays need an items schema")
		}
		return inspectSchema(child, depth+1)
	case "string", "number", "integer", "boolean", "null":
	default:
		return errors.New("unsupported outputSchema type")
	}
	return nil
}
func validateJSONOutput(raw json.RawMessage, text string) error {
	var schema map[string]any
	source := json.NewDecoder(bytes.NewReader(raw))
	source.UseNumber()
	_ = source.Decode(&schema)
	decoder := json.NewDecoder(bytes.NewBufferString(text))
	decoder.UseNumber()
	var value any
	if e := decoder.Decode(&value); e != nil || decoder.Decode(new(any)) != io.EOF {
		return errors.New("Codex returned invalid JSON")
	}
	return matchSchema(schema, value)
}
func matchSchema(schema map[string]any, value any) error {
	valid := false
	switch schema["type"] {
	case "object":
		if object, ok := value.(map[string]any); ok {
			properties := schema["properties"].(map[string]any)
			valid = len(object) == len(properties)
			for key, child := range properties {
				v, exists := object[key]
				if !exists || matchSchema(child.(map[string]any), v) != nil {
					valid = false
					break
				}
			}
		}
	case "array":
		if array, ok := value.([]any); ok {
			valid = true
			for _, v := range array {
				if matchSchema(schema["items"].(map[string]any), v) != nil {
					valid = false
					break
				}
			}
		}
	case "string":
		_, valid = value.(string)
	case "boolean":
		_, valid = value.(bool)
	case "number":
		if number, ok := value.(json.Number); ok {
			_, _, e := canonicalNumber(number)
			valid = e == nil
		}
	case "integer":
		if v, ok := value.(json.Number); ok {
			_, exponent, e := canonicalNumber(v)
			valid = e == nil && exponent >= 0
		}
	case "null":
		valid = value == nil
	}
	if !valid {
		return errors.New("Codex output did not match outputSchema")
	}
	if list, ok := schema["enum"].([]any); ok {
		for _, v := range list {
			if jsonValueEqual(v, value) {
				return nil
			}
		}
		return errors.New("Codex output did not match the schema enum")
	}
	return nil
}

func canonicalNumber(number json.Number) (string, int64, error) {
	text := strings.ToLower(number.String())
	if len(text) > 256 {
		return "", 0, errors.New("number exceeds supported precision")
	}
	base, exponentText, hasExponent := strings.Cut(text, "e")
	var exponent int64
	var err error
	if hasExponent {
		exponent, err = strconv.ParseInt(exponentText, 10, 32)
		if err != nil || exponent > 1000000 || exponent < -1000000 {
			return "", 0, errors.New("number exponent exceeds supported range")
		}
	}
	whole, fraction, _ := strings.Cut(base, ".")
	digits := whole + fraction
	exponent -= int64(len(fraction))
	negative := strings.HasPrefix(digits, "-")
	digits = strings.TrimPrefix(digits, "-")
	digits = strings.TrimLeft(digits, "0")
	if digits == "" {
		return "0", 0, nil
	}
	trimmed := strings.TrimRight(digits, "0")
	exponent += int64(len(digits) - len(trimmed))
	if negative {
		trimmed = "-" + trimmed
	}
	return trimmed, exponent, nil
}
func jsonValueEqual(a, b any) bool {
	if left, ok := a.(json.Number); ok {
		right, ok := b.(json.Number)
		if !ok {
			return false
		}
		lm, le, err := canonicalNumber(left)
		rm, re, other := canonicalNumber(right)
		return err == nil && other == nil && lm == rm && le == re
	}
	if left, ok := a.(map[string]any); ok {
		right, ok := b.(map[string]any)
		if !ok || len(left) != len(right) {
			return false
		}
		for k, v := range left {
			r, exists := right[k]
			if !exists || !jsonValueEqual(v, r) {
				return false
			}
		}
		return true
	}
	if left, ok := a.([]any); ok {
		right, ok := b.([]any)
		if !ok || len(left) != len(right) {
			return false
		}
		for i, v := range left {
			if !jsonValueEqual(v, right[i]) {
				return false
			}
		}
		return true
	}
	return reflect.DeepEqual(a, b)
}
