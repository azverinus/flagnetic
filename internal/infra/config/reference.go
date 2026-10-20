package config

import (
	"bytes"
	"fmt"
	"strings"
)

// Reference returns the configuration reference, docs/configuration.md, as
// generated from the settings table.
func Reference() []byte {
	var b bytes.Buffer
	b.WriteString(`# Configuration reference

<!-- Generated from the settings table in internal/infra/config by ` + "`task generate`" + `. Do not edit. -->

Flagnetic is configured with environment variables and, optionally, a YAML file named by ` + "`FLAGNETIC_CONFIG_FILE`" + `. Each setting is read from its environment variable when that is set, even to the empty string; otherwise from the file; otherwise it takes its default. An empty value means the default, except where a setting says it switches something off.

The file is a mapping from the keys in the File key column to single values:

` + "```yaml" + `
database_url: postgres://flagnetic@db:5432/flagnetic?sslmode=require
listen_addr: ":8080"
log_format: text
` + "```" + `

A file key that is not listed here, a value that is not a single value, and a value that does not parse are refused when the server starts, with exit code 2 and a message naming the setting.

Durations are written as a number and a unit: ` + "`500ms`, `15s`, `2m`" + `.

| Variable | File key | Default | Description |
|---|---|---|---|
`)
	for _, s := range Settings() {
		key, def := "—", "—"
		if s.Key != "" {
			key = "`" + s.Key + "`"
		}
		switch {
		case s.Required:
			def = "required"
		case s.Default != "":
			def = "`" + s.Default + "`"
		}
		desc := s.Description
		if s.Unit != "" {
			desc += " Unit: " + s.Unit + "."
		}
		desc += " Example: `" + s.Example + "`."
		fmt.Fprintf(&b, "| `%s` | %s | %s | %s |\n", s.Name, key, def, strings.ReplaceAll(desc, "|", `\|`))
	}
	return b.Bytes()
}
