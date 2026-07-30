// Package scenarios embeds the six MVP scenario template definitions into the
// single InterviewCraft binary.
package scenarios

import (
	"embed"
	"fmt"
)

// Files contains the versioned JSON template assets.
//
//go:embed *.json
var Files embed.FS

var names = []string{
	"behavioral",
	"project_deep_dive",
	"technical_foundations",
	"algorithm_coding",
	"system_design",
	"mixed",
}

// Names returns the stable product order of all MVP templates.
func Names() []string {
	return append([]string(nil), names...)
}

// Read returns one embedded template definition.
func Read(name string) ([]byte, error) {
	for _, candidate := range names {
		if candidate == name {
			return Files.ReadFile(candidate + ".json")
		}
	}
	return nil, fmt.Errorf("unknown scenario template %q", name)
}
