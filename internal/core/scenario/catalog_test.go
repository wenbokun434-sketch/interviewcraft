package scenario

import (
	"reflect"
	"testing"

	"github.com/interviewcraft/interviewcraft/internal/core/contracts"
)

func TestEmbeddedCatalogContainsSixOrderedTemplates(t *testing.T) {
	t.Parallel()

	templates, err := LoadTemplates()
	if err != nil {
		t.Fatalf("LoadTemplates: %v", err)
	}
	got := make([]string, len(templates))
	for index, template := range templates {
		got[index] = template.ID
		if err := template.Validate(); err != nil {
			t.Errorf("template %q: %v", template.ID, err)
		}
		if template.DefaultMode != contracts.ScenarioStrict &&
			template.DefaultMode != contracts.ScenarioStandard &&
			template.DefaultMode != contracts.ScenarioCoach {
			t.Errorf("template %q mode = %q", template.ID, template.DefaultMode)
		}
	}
	want := []string{
		"behavioral",
		"project_deep_dive",
		"technical_foundations",
		"algorithm_coding",
		"system_design",
		"mixed",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("template IDs = %#v, want %#v", got, want)
	}
}

func TestTemplateCatalogReturnsDefensiveCopies(t *testing.T) {
	t.Parallel()

	service, err := NewService(nil, nil, nil)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	first := service.Templates()
	first[0].Label = "mutated"
	first[0].QuestionGuidance[0] = "mutated"

	second := service.Templates()

	if second[0].Label == "mutated" ||
		second[0].QuestionGuidance[0] == "mutated" {
		t.Fatal("Templates returned shared mutable data")
	}
}
