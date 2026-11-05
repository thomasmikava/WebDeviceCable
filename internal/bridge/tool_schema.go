package bridge

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"strings"
	"unicode/utf8"
)

// Tool inputs have their own portable contract. Output schemas intentionally
// retain the stricter structured-output contract in native_json.go.
func checkToolSchema(raw json.RawMessage) error {
	if len(raw) == 0 || len(raw) > 8192 {
		return errors.New("inputSchema must be at most 8 KiB")
	}
	schema, err := readToolJSON(raw)
	if err != nil {
		return errors.New("inputSchema must be a JSON object")
	}
	object, ok := schema.(map[string]any)
	if !ok || object["type"] != "object" {
		return errors.New("inputSchema root must have type object")
	}
	return inspectToolSchema(object, 0)
}

func readToolJSON(raw []byte) (any, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	if decoder.Decode(new(any)) != io.EOF {
		return nil, errors.New("trailing JSON")
	}
	return value, nil
}

func toolTypes(value any) ([]string, error) {
	if value == nil {
		return nil, nil
	}
	values, ok := value.([]any)
	if !ok {
		values = []any{value}
	}
	if len(values) == 0 || len(values) > 7 {
		return nil, errors.New("invalid inputSchema type")
	}
	types := []string{}
	seen := map[string]bool{}
	for _, value := range values {
		typ, ok := value.(string)
		if !ok || seen[typ] {
			return nil, errors.New("invalid inputSchema type")
		}
		switch typ {
		case "object", "array", "string", "number", "integer", "boolean", "null":
		default:
			return nil, errors.New("invalid inputSchema type")
		}
		seen[typ] = true
		types = append(types, typ)
	}
	return types, nil
}

func inspectToolSchema(schema map[string]any, depth int) error {
	if depth > 12 {
		return errors.New("inputSchema exceeds 12 levels")
	}
	for key := range schema {
		switch key {
		case "type", "properties", "required", "additionalProperties", "items", "enum", "const", "anyOf", "description", "title", "minimum", "maximum", "minLength", "maxLength", "minItems", "maxItems":
		default:
			return fmt.Errorf("unsupported inputSchema keyword: %s", key)
		}
	}
	if value, exists := schema["type"]; exists && value == nil {
		return errors.New("invalid inputSchema type")
	}
	if _, err := toolTypes(schema["type"]); err != nil {
		return err
	}
	for _, key := range []string{"title", "description"} {
		if value, exists := schema[key]; exists {
			if _, ok := value.(string); !ok {
				return fmt.Errorf("inputSchema %s must be text", key)
			}
		}
	}
	if value, exists := schema["enum"]; exists {
		list, ok := value.([]any)
		if !ok || len(list) == 0 || len(list) > 64 {
			return errors.New("inputSchema enum needs 1–64 values")
		}
	}
	if value, exists := schema["properties"]; exists {
		properties, ok := value.(map[string]any)
		if !ok || len(properties) > 64 {
			return errors.New("inputSchema allows at most 64 properties per object")
		}
		for _, child := range properties {
			if err := inspectToolChild(child, depth); err != nil {
				return err
			}
		}
	}
	if value, exists := schema["required"]; exists {
		list, ok := value.([]any)
		if !ok || len(list) > 64 {
			return errors.New("invalid inputSchema required properties")
		}
		seen := map[string]bool{}
		for _, key := range list {
			name, ok := key.(string)
			if !ok || seen[name] {
				return errors.New("invalid inputSchema required properties")
			}
			seen[name] = true
		}
	}
	if value, exists := schema["additionalProperties"]; exists {
		if _, ok := value.(bool); !ok {
			if err := inspectToolChild(value, depth); err != nil {
				return err
			}
		}
	}
	if value, exists := schema["items"]; exists {
		if err := inspectToolChild(value, depth); err != nil {
			return err
		}
	}
	if value, exists := schema["anyOf"]; exists {
		list, ok := value.([]any)
		if !ok || len(list) == 0 || len(list) > 16 {
			return errors.New("inputSchema anyOf needs 1–16 branches")
		}
		for _, child := range list {
			if err := inspectToolChild(child, depth); err != nil {
				return err
			}
		}
	}
	for _, pair := range [][2]string{{"minimum", "maximum"}, {"minLength", "maxLength"}, {"minItems", "maxItems"}} {
		for _, key := range pair {
			if value, exists := schema[key]; exists {
				number, ok := value.(json.Number)
				if !ok {
					return fmt.Errorf("inputSchema %s must be a number", key)
				}
				_, exponent, err := canonicalNumber(number)
				if err != nil {
					return fmt.Errorf("inputSchema %s: %w", key, err)
				}
				f, err := number.Float64()
				if err != nil || math.IsInf(f, 0) {
					return errors.New("inputSchema bounds must be finite numbers")
				}
				if key != "minimum" && key != "maximum" {
					if exponent < 0 || f < 0 || f > 9007199254740991 {
						return fmt.Errorf("inputSchema %s must be a nonnegative safe integer", key)
					}
				}
			}
		}
		if low, exists := schema[pair[0]]; exists {
			if high, exists := schema[pair[1]]; exists {
				order, err := compareJSONNumbers(low.(json.Number), high.(json.Number))
				if err != nil {
					return err
				}
				if order > 0 {
					return errors.New("inputSchema minimum exceeds maximum")
				}
			}
		}
	}
	return nil
}

func inspectToolChild(value any, depth int) error {
	child, ok := value.(map[string]any)
	if !ok {
		return errors.New("inputSchema children must be schema objects")
	}
	return inspectToolSchema(child, depth+1)
}

func validateToolArguments(raw, arguments json.RawMessage) error {
	schema, err := readToolJSON(raw)
	if err != nil {
		return err
	}
	value, err := readToolJSON(arguments)
	if err != nil {
		return err
	}
	return matchToolSchema(schema.(map[string]any), value)
}

func matchToolSchema(schema map[string]any, value any) error {
	invalid := errors.New("arguments do not match inputSchema")
	if types, _ := toolTypes(schema["type"]); len(types) > 0 {
		valid := false
		for _, typ := range types {
			switch typ {
			case "object":
				_, valid = value.(map[string]any)
			case "array":
				_, valid = value.([]any)
			case "string":
				_, valid = value.(string)
			case "boolean":
				_, valid = value.(bool)
			case "null":
				valid = value == nil
			case "number":
				_, valid = value.(json.Number)
			case "integer":
				if number, ok := value.(json.Number); ok {
					_, exponent, err := canonicalNumber(number)
					valid = err == nil && exponent >= 0
				}
			}
			if valid {
				break
			}
		}
		if !valid {
			return invalid
		}
	}
	if constant, exists := schema["const"]; exists && !jsonValueEqual(constant, value) {
		return invalid
	}
	if list, exists := schema["enum"].([]any); exists {
		found := false
		for _, item := range list {
			if jsonValueEqual(item, value) {
				found = true
				break
			}
		}
		if !found {
			return invalid
		}
	}
	if branches, exists := schema["anyOf"].([]any); exists {
		found := false
		for _, branch := range branches {
			if matchToolSchema(branch.(map[string]any), value) == nil {
				found = true
				break
			}
		}
		if !found {
			return invalid
		}
	}
	switch value := value.(type) {
	case map[string]any:
		properties, _ := schema["properties"].(map[string]any)
		if required, ok := schema["required"].([]any); ok {
			for _, key := range required {
				if _, exists := value[key.(string)]; !exists {
					return invalid
				}
			}
		}
		for key, item := range value {
			if child, exists := properties[key]; exists {
				if matchToolSchema(child.(map[string]any), item) != nil {
					return invalid
				}
			} else if additional, exists := schema["additionalProperties"]; exists {
				if allowed, ok := additional.(bool); ok {
					if !allowed {
						return invalid
					}
				} else if matchToolSchema(additional.(map[string]any), item) != nil {
					return invalid
				}
			}
		}
	case []any:
		if !toolLength(schema, len(value), "minItems", "maxItems") {
			return invalid
		}
		if child, exists := schema["items"].(map[string]any); exists {
			for _, item := range value {
				if matchToolSchema(child, item) != nil {
					return invalid
				}
			}
		}
	case string:
		if !toolLength(schema, utf8.RuneCountInString(value), "minLength", "maxLength") {
			return invalid
		}
	case json.Number:
		if _, _, err := canonicalNumber(value); err != nil {
			return invalid
		}
		if low, exists := schema["minimum"].(json.Number); exists {
			if order, err := compareJSONNumbers(value, low); err != nil || order < 0 {
				return invalid
			}
		}
		if high, exists := schema["maximum"].(json.Number); exists {
			if order, err := compareJSONNumbers(value, high); err != nil || order > 0 {
				return invalid
			}
		}
	}
	return nil
}

func toolLength(schema map[string]any, length int, low, high string) bool {
	if value, ok := schema[low].(json.Number); ok {
		f, _ := value.Float64()
		n := int64(f)
		if int64(length) < n {
			return false
		}
	}
	if value, ok := schema[high].(json.Number); ok {
		f, _ := value.Float64()
		n := int64(f)
		if int64(length) > n {
			return false
		}
	}
	return true
}

func compareJSONNumbers(left, right json.Number) (int, error) {
	l, le, err := canonicalNumber(left)
	if err != nil {
		return 0, err
	}
	r, re, err := canonicalNumber(right)
	if err != nil {
		return 0, err
	}
	if l == r && le == re {
		return 0, nil
	}
	ln, rn := strings.HasPrefix(l, "-"), strings.HasPrefix(r, "-")
	if ln != rn {
		if ln {
			return -1, nil
		}
		return 1, nil
	}
	l, r = strings.TrimPrefix(l, "-"), strings.TrimPrefix(r, "-")
	result := 0
	if l == "0" {
		result = -1
	} else if r == "0" {
		result = 1
	} else if int64(len(l))+le < int64(len(r))+re {
		result = -1
	} else if int64(len(l))+le > int64(len(r))+re {
		result = 1
	} else {
		n := max(len(l), len(r))
		result = strings.Compare(l+strings.Repeat("0", n-len(l)), r+strings.Repeat("0", n-len(r)))
	}
	if ln {
		return -result, nil
	}
	return result, nil
}
