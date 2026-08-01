// Package coding embeds the versioned coding-question catalog in the single
// InterviewCraft binary.
package coding

import (
	"embed"
	"fmt"
)

// Files contains coding-question assets.
//
//go:embed *.json
var Files embed.FS

var names = []string{"pair_sum"}

// Names returns the stable catalog order.
func Names() []string {
	return append([]string(nil), names...)
}

// Read returns one known embedded question definition.
func Read(name string) ([]byte, error) {
	for _, candidate := range names {
		if candidate == name {
			return Files.ReadFile(candidate + ".json")
		}
	}
	return nil, fmt.Errorf("unknown coding question %q", name)
}
