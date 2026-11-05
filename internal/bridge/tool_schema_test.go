package bridge

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestToolInputSchemasAreIndependentOfOutputs(t *testing.T) {
	schema := json.RawMessage(`{"type":"object","properties":{"operation":{"type":"string","minLength":1},"arguments":{"type":"object","additionalProperties":true},"note":{"type":["string","null"]},"limit":{"anyOf":[{"type":"integer","minimum":1,"maximum":10},{"type":"null"}]}},"required":["operation","arguments"],"additionalProperties":false}`)
	if err := checkToolSchema(schema); err != nil {
		t.Fatal(err)
	}
	if err := checkOutputSchema(schema); err == nil {
		t.Fatal("output schema restrictions were loosened")
	}
	for _, value := range []string{`{"operation":"rename","arguments":{"arbitrary":{"nested":[1,true,null]} }}`, `{"operation":"rename","arguments":{},"note":null,"limit":1e1}`} {
		if err := validateToolArguments(schema, json.RawMessage(value)); err != nil {
			t.Fatal(value, err)
		}
	}
	for _, value := range []string{`{"operation":"","arguments":{}}`, `{"operation":"rename"}`, `{"operation":"rename","arguments":{},"note":2}`, `{"operation":"rename","arguments":{},"limit":11}`, `{"operation":"rename","arguments":{},"extra":true}`} {
		if err := validateToolArguments(schema, json.RawMessage(value)); err == nil {
			t.Fatal("accepted", value)
		}
	}
}

func TestToolSchemaBoundariesAndKeywords(t *testing.T) {
	for _, raw := range []string{`{"type":"object","$ref":"https://example.test/schema"}`, `{"type":"object","properties":{"a":{"type":["string","string"]}}}`, `{"type":"object","anyOf":[]}`, `{"type":"object","properties":{"a":{"type":"string","minLength":-1}}}`, `{"type":"object","properties":{"a":{"minimum":2,"maximum":1}}}`, `{"type":"object","required":["a","a"]}`} {
		if checkToolSchema(json.RawMessage(raw)) == nil {
			t.Fatal("accepted", raw)
		}
	}
	deep := `{"type":"string"}`
	for i := 0; i < 13; i++ {
		deep = `{"type":"object","properties":{"x":` + deep + `}}`
	}
	if checkToolSchema(json.RawMessage(deep)) == nil {
		t.Fatal("accepted deep schema")
	}
	if checkToolSchema(json.RawMessage(`{"type":"object","description":"`+strings.Repeat("x", 8192)+`"}`)) == nil {
		t.Fatal("accepted oversized schema")
	}
	schema := json.RawMessage(`{"type":"object","additionalProperties":{"anyOf":[{"type":"string","minLength":1,"maxLength":1},{"const":2}]}}`)
	if err := checkToolSchema(schema); err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{`{"a":"🙂","b":2.0}`, `{"a":"x"}`} {
		if err := validateToolArguments(schema, json.RawMessage(value)); err != nil {
			t.Fatal(err)
		}
	}
	for _, value := range []string{`{"a":"ab"}`, `{"a":3}`} {
		if validateToolArguments(schema, json.RawMessage(value)) == nil {
			t.Fatal("accepted", value)
		}
	}
}

func TestToolSchemaRejectsUncomparableBounds(t *testing.T) {
	for _, keyword := range []string{"minimum", "maximum", "minLength", "maxLength", "minItems", "maxItems"} {
		for _, number := range []string{"1." + strings.Repeat("0", 300), "0e1000001", "1e-1000001", "1e-2147483649"} {
			t.Run(keyword+"/"+number[:min(len(number), 24)], func(t *testing.T) {
				schema := json.RawMessage(`{"type":"object","properties":{"value":{"` + keyword + `":` + number + `}}}`)
				if checkToolSchema(schema) == nil {
					t.Fatal("admitted a bound outside the numeric validator's representation limits")
				}
			})
		}
	}
	// Validation must also fail closed for an unsupported bound rather than
	// treating a failed numeric normalization as an ordinary coefficient.
	schema := json.RawMessage(`{"type":"object","properties":{"value":{"type":"number","minimum":1.` + strings.Repeat("0", 300) + `}}}`)
	if validateToolArguments(schema, json.RawMessage(`{"value":0.1}`)) == nil {
		t.Fatal("accepted 0.1 despite minimum=1")
	}
}

func TestToolSchemaNumericBoundsPreserveDecimalPrecision(t *testing.T) {
	for _, test := range []struct {
		bounds   string
		accepted []string
		rejected []string
	}{
		{`"minimum":1.` + strings.Repeat("0", 254) + `,"maximum":1`, []string{"1", "1.0", "10e-1"}, []string{"0.1", "1.00000000000000001", "1.1"}},
		{`"minimum":9007199254740993,"maximum":9007199254740993`, []string{"9007199254740993", "90071992547409930e-1"}, []string{"9007199254740992", "9007199254740994"}},
		{`"minimum":-0.00000000000000000001,"maximum":-0.00000000000000000001`, []string{"-1e-20", "-0.00000000000000000001"}, []string{"0", "-2e-20", "-1e-21"}},
	} {
		schema := json.RawMessage(`{"type":"object","properties":{"value":{"type":"number",` + test.bounds + `}}}`)
		if err := checkToolSchema(schema); err != nil {
			t.Fatal(err)
		}
		for _, number := range test.accepted {
			if err := validateToolArguments(schema, json.RawMessage(`{"value":`+number+`}`)); err != nil {
				t.Fatalf("rejected %s: %v", number, err)
			}
		}
		for _, number := range test.rejected {
			if validateToolArguments(schema, json.RawMessage(`{"value":`+number+`}`)) == nil {
				t.Fatalf("accepted %s for bounds %s", number, test.bounds)
			}
		}
	}
	for _, keyword := range []string{"minLength", "maxLength", "minItems", "maxItems"} {
		for _, number := range []string{"1.00000000000000001", "-1e-400", "9007199254740992"} {
			schema := json.RawMessage(`{"type":"object","properties":{"value":{"` + keyword + `":` + number + `}}}`)
			if checkToolSchema(schema) == nil {
				t.Fatal("admitted a length bound that is not a nonnegative safe integer", keyword, number)
			}
		}
	}
}
