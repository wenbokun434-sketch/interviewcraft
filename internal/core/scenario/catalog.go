package scenario

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	scenariocontent "github.com/interviewcraft/interviewcraft/content/scenarios"
)

// LoadTemplates decodes and validates the six embedded template assets.
func LoadTemplates() ([]Template, error) {
	names := scenariocontent.Names()
	templates := make([]Template, 0, len(names))
	seen := make(map[string]struct{}, len(names))
	for _, name := range names {
		payload, err := scenariocontent.Read(name)
		if err != nil {
			return nil, fmt.Errorf("read template %q: %w", name, err)
		}
		var template Template
		decoder := json.NewDecoder(bytes.NewReader(payload))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&template); err != nil {
			return nil, fmt.Errorf("decode template %q: %w", name, err)
		}
		var extra any
		if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
			if err == nil {
				err = errors.New("multiple JSON values")
			}
			return nil, fmt.Errorf("decode template %q: %w", name, err)
		}
		if template.ID != name {
			return nil, fmt.Errorf(
				"template file %q declares id %q",
				name,
				template.ID,
			)
		}
		if err := template.Validate(); err != nil {
			return nil, err
		}
		if _, duplicate := seen[template.ID]; duplicate {
			return nil, fmt.Errorf("duplicate template %q", template.ID)
		}
		seen[template.ID] = struct{}{}
		templates = append(templates, template)
	}
	if len(templates) != 6 {
		return nil, fmt.Errorf("template catalog contains %d entries, want 6", len(templates))
	}
	return templates, nil
}
