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
	b.WriteString(`---
title: "Configuration reference"
description: "Every setting of a Flagnetic replica, as an environment variable or a configuration file key, with its default, and the variables the command line reads, generated from the settings table."
---

# Configuration reference

<!-- Generated from the settings table in internal/infra/config by ` + "`task generate`" + `. Do not edit. -->

Flagnetic is configured with environment variables and, optionally, a YAML file named by ` + "`FLAGNETIC_CONFIG_FILE`" + `. Each setting is read from its environment variable when that is set, even to the empty string; otherwise from the file; otherwise it takes its default. An empty value means the default, except where a setting says it switches something off.

The file is a mapping from the keys in the File key column to single values:

` + "```yaml" + `
database_url: postgres://flagnetic@db:5432/flagnetic?sslmode=require
listen_addr: ":8080"
log_format: text
` + "```" + `

A value that is not a single value, a value that does not parse or lies outside the range shown, a key that is not written like a setting name and the key of a setting that only the environment sets are refused when the replica starts, with exit code 2 and a message naming the setting. A well-formed key that is not listed here is ignored with a warning in the log, and so is an environment variable that starts with ` + "`FLAGNETIC_`" + ` but is no setting and no variable of the command line: a binary rolled back to an earlier version still starts with the file or the environment written for a newer one, and a mistyped setting is noticed.

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
		case s.RequiredBy != "":
			def = "required by `" + s.RequiredBy + "`"
		case s.Default != "":
			def = "`" + s.Default + "`"
		}
		desc := s.Description
		if s.Unit != "" {
			desc += " Unit: " + s.Unit + "."
		}
		if s.Range != "" {
			desc += " Range: " + s.Range + "."
		}
		desc += " Example: `" + s.Example + "`."
		fmt.Fprintf(&b, "| `%s` | %s | %s | %s |\n", s.Name, key, def, strings.ReplaceAll(desc, "|", `\|`))
	}
	b.WriteString("\n## Variables of the command line\n\nThe table above is the settings of a replica. The commands read these variables too, never `serve`; they are environment only. The command line's own flags are listed by each command's `-h`.\n\n| Variable | Description |\n|---|---|\n")
	for _, v := range CommandLineVariables() {
		fmt.Fprintf(&b, "| `%s` | %s |\n", v.Name, strings.ReplaceAll(v.Description, "|", `\|`))
	}
	b.WriteString("\nSee [The first instance admin](operations.md#the-first-instance-admin) for the admin password.\n")
	return b.Bytes()
}
