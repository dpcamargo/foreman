package schemas_test

import (
	"encoding/json"
	"testing"

	"github.com/dpcamargo/foreman/schemas"
)

func TestSchemasAreValidJSON(t *testing.T) {
	cases := []struct {
		name string
		data []byte
	}{
		{"ArmResult", schemas.ArmResult},
		{"TaskSpec", schemas.TaskSpec},
		{"ReviewFindings", schemas.ReviewFindings},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var parsed map[string]any
			if err := json.Unmarshal(tc.data, &parsed); err != nil {
				t.Fatalf("schema %s is not valid JSON: %v", tc.name, err)
			}
			if _, ok := parsed["properties"]; !ok {
				t.Errorf("schema %s missing properties", tc.name)
			}
			if typ, ok := parsed["type"]; !ok || typ != "object" {
				t.Errorf("schema %s type is not object: %v", tc.name, typ)
			}
		})
	}
}
