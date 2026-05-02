package openapi

import "sigs.k8s.io/yaml"

// jsonToYAMLFn is a test seam: assigned via t.Cleanup in unit tests that
// need to exercise the defensive YAML / JSON marshal failure branches that
// normal inputs cannot reach. Production code always calls through this
// indirection.
var jsonToYAMLFn = yaml.JSONToYAML

// jsonToYAML wraps sigs.k8s.io/yaml.JSONToYAML so the rest of the
// package can call into a stable internal name. Using JSONToYAML (rather
// than Marshal on the Go value directly) keeps key ordering identical to
// the JSON spec — sigs.k8s.io/yaml internally re-marshals through JSON,
// preserving encoding/json's deterministic map key ordering.
func jsonToYAML(jsonBytes []byte) ([]byte, error) {
	return jsonToYAMLFn(jsonBytes)
}
