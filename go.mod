module github.com/plheide/aapi-codegen

go 1.26

require (
	github.com/atombender/go-jsonschema v0.0.0-00010101000000-000000000000
	gopkg.in/yaml.v3 v3.0.1
)

require (
	dario.cat/mergo v1.0.2 // indirect
	github.com/goccy/go-yaml v1.19.2 // indirect
	github.com/google/go-cmp v0.7.0 // indirect
	github.com/mitchellh/go-wordwrap v1.0.1 // indirect
	github.com/sanity-io/litter v1.5.8 // indirect
	github.com/sosodev/duration v1.4.0 // indirect
)

// The patched local fork at ../go-jsonschema is the canonical
// implementation aapi-codegen builds against. Upstream may not yet
// expose every flag the local fork does — when it catches up, drop the
// replace and pin a tag.
replace github.com/atombender/go-jsonschema => ../go-jsonschema
