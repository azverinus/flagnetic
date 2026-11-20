package management_test

import (
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/azverinus/flagnetic/internal/domain/workspace"
)

type documentSchema struct {
	Ref                  string                    `yaml:"$ref"`
	Type                 string                    `yaml:"type"`
	Enum                 []string                  `yaml:"enum"`
	Properties           map[string]documentSchema `yaml:"properties"`
	Items                *documentSchema           `yaml:"items"`
	AdditionalProperties any                       `yaml:"additionalProperties"`
	AllOf                []documentSchema          `yaml:"allOf"`
}

func readDocumentSchemas(t *testing.T) map[string]documentSchema {
	t.Helper()
	raw, err := os.ReadFile("../../../../../api/management.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Components struct {
			Schemas map[string]documentSchema `yaml:"schemas"`
		} `yaml:"components"`
	}
	if err = yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	return doc.Components.Schemas
}

// TestTheDocumentSchemaMatchesWhatTheServerAcceptsAndExports holds the
// published schema to the decoder: the attribute types it lists are the
// types an attribute is declared with, and every field of an exported
// document is one the schema names, with unknown fields refused as the
// server refuses them.
func TestTheDocumentSchemaMatchesWhatTheServerAcceptsAndExports(t *testing.T) {
	schemas := readDocumentSchemas(t)

	var want []string
	for _, a := range workspace.AttributeTypes() {
		want = append(want, string(a))
	}
	got := slices.Clone(schemas["DocumentAttribute"].Properties["type"].Enum)
	slices.Sort(want)
	slices.Sort(got)
	if !slices.Equal(got, want) {
		t.Errorf("DocumentAttribute type enum = %v, want the attribute types %v", got, want)
	}

	for name, s := range schemas {
		if strings.HasPrefix(name, "Document") && name != "DocumentFormat" && s.Type == "object" && s.AdditionalProperties != false {
			t.Errorf("%s does not set additionalProperties: false, so a validator accepts files the server refuses", name)
		}
	}

	f := exportedProject(t)
	var check func(path string, value any, s documentSchema)
	check = func(path string, value any, s documentSchema) {
		if s.Ref != "" {
			s = schemas[strings.TrimPrefix(s.Ref, "#/components/schemas/")]
		}
		for _, a := range s.AllOf {
			check(path, value, a)
		}
		switch v := value.(type) {
		case map[string]any:
			if s.Properties == nil {
				return
			}
			for k, child := range v {
				cs, ok := s.Properties[k]
				if !ok {
					t.Errorf("%s/%s is in an export but not in the schema", path, k)
					continue
				}
				check(path+"/"+k, child, cs)
			}
		case []any:
			if s.Items != nil {
				for i, child := range v {
					check(path+"/"+strconv.Itoa(i), child, *s.Items)
				}
			}
		}
	}
	check("", exported(t, f.admin, f.project.Key), documentSchema{Ref: "#/components/schemas/ProjectDocument"})
}
