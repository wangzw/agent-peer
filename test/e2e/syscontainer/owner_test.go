package syscontainer

import (
	"os"
	"regexp"
	"testing"

	"github.com/wangzw/peers/test/e2e/harness"
	"gopkg.in/yaml.v3"
)

// TestOwnerLabelValue fixes the label value to the owner marker form, which
// harness.CleanResidue parses to tell a live suite from residue.
func TestOwnerLabelValue(t *testing.T) {
	pattern := regexp.MustCompile(`^ape2e-[0-9]+-[0-9]+-pidns[0-9]+$`)
	if value := ownerLabelValue(); !pattern.MatchString(value) {
		t.Fatalf("ownerLabelValue() = %q, want a match for %s", value, pattern)
	}
}

// TestSystemComposeCarriesOwnerLabel checks that the compose file puts the
// owner label on every resource the cleanup removes: each service, the pair
// volume and the system network. Without the label a project that keeps its
// readable name would look like an unknown owner and never be cleaned.
func TestSystemComposeCarriesOwnerLabel(t *testing.T) {
	data, err := os.ReadFile("../compose/system/compose.yaml")
	if err != nil {
		t.Fatalf("reading the system compose file: %v", err)
	}

	type labelled struct {
		Labels map[string]string `yaml:"labels"`
	}
	var doc struct {
		Services map[string]labelled `yaml:"services"`
		Volumes  map[string]labelled `yaml:"volumes"`
		Networks map[string]labelled `yaml:"networks"`
	}
	if err := yaml.Unmarshal(data, &doc); err != nil {
		t.Fatalf("parsing the system compose file: %v", err)
	}
	if len(doc.Services) == 0 || len(doc.Volumes) == 0 || len(doc.Networks) == 0 {
		t.Fatalf("the compose file declares %d services, %d volumes and %d networks; want at least one of each",
			len(doc.Services), len(doc.Volumes), len(doc.Networks))
	}

	want := "${" + ownerEnv + ":?}"
	for _, group := range []struct {
		kind     string
		labelled map[string]labelled
	}{
		{"service", doc.Services},
		{"volume", doc.Volumes},
		{"network", doc.Networks},
	} {
		for name, resource := range group.labelled {
			if got := resource.Labels[harness.OwnerLabel]; got != want {
				t.Errorf("%s %s: label %s = %q, want %q", group.kind, name, harness.OwnerLabel, got, want)
			}
		}
	}
}
