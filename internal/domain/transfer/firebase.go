package transfer

import (
	"maps"

	"github.com/azverinus/flagnetic/internal/domain/transfer/firebase"
)

// SourceFirebase is the source of an import that read a Remote Config
// template.
const SourceFirebase = "firebase"

// FirebaseDocument returns the document that merges a translated template into
// the environment of a project whose document is current: the attributes the
// translation declares and a flag for every parameter, and nothing else, so
// that a merge leaves the rest of the project as it is.
//
// A flag the project has is written whole, so it is its current entry with
// only what the translation sets changed: the description, the tags, the
// variations and its configuration in the environment, whose prerequisites
// and overrides are kept. Its name, owner, lifetime, archiving and every other
// environment stay. A new flag is configured in the environment only, on, and
// the merge gives it its initial configuration, off, in the others.
func FirebaseDocument(current Document, res firebase.Result, environment string) Document {
	existing := make(map[string]Flag, len(current.Flags))
	for _, f := range current.Flags {
		existing[f.Key] = f
	}
	out := Document{
		SchemaVersion: Version,
		Attributes:    make([]Attribute, 0, len(res.Attributes)),
		Flags:         make([]Flag, 0, len(res.Flags)),
	}
	for _, a := range res.Attributes {
		out.Attributes = append(out.Attributes, Attribute{Key: a.Key, Type: string(a.Type), Name: a.Name, Description: a.Description})
	}
	for _, f := range res.Flags {
		doc := Flag{Key: f.Key, Type: string(f.Type), Name: f.Name, Configurations: map[string]Configuration{}}
		if was, ok := existing[f.Key]; ok {
			doc = was
			doc.Configurations = make(map[string]Configuration, len(was.Configurations)+1)
			maps.Copy(doc.Configurations, was.Configurations)
		}
		doc.Description, doc.Tags = f.Description, f.Tags
		doc.Variations = make([]Variation, len(f.Variations))
		for i, v := range f.Variations {
			doc.Variations[i] = Variation{Key: v.Key, Value: v.Value, Name: v.Name, Description: v.Description}
		}
		c := doc.Configurations[environment]
		c.On, c.OffVariation, c.Fallthrough = f.On, f.OffVariation, f.Fallthrough
		c.Rules = make([]Rule, len(f.Rules))
		for i, r := range f.Rules {
			c.Rules[i] = Rule{Description: r.Description, Conditions: conditionsOf(r.Conditions), Serve: serveOf(r.Serve)}
			if !r.ID.IsZero() {
				c.Rules[i].ID = r.ID.String()
			}
		}
		if c.Prerequisites == nil {
			c.Prerequisites = []Prerequisite{}
		}
		if c.Overrides == nil {
			c.Overrides = []Override{}
		}
		doc.Configurations[environment] = c
		out.Flags = append(out.Flags, doc)
	}
	return out
}

// FirebaseWarnings returns the warnings of a translation as an import
// answers them, their pointers into the template.
func FirebaseWarnings(in []firebase.Warning) []Warning {
	out := make([]Warning, len(in))
	for i, w := range in {
		out[i] = Warning{Code: w.Code, Pointer: w.Pointer, Detail: w.Detail, Params: w.Params}
	}
	return out
}
