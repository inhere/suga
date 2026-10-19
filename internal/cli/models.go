package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/gookit/goutil/cflag/capp"
	appconfig "github.com/inhere/suga/internal/config"
)

// newModelsCommand renders the local model projection. It deliberately calls
// only configuration readers: no runtime, provider, MCP, hook, or network code
// is involved in this command.
func newModelsCommand(stdout, stderr io.Writer) *capp.Cmd {
	var asJSON bool
	models := capp.NewCmd("models", "List configured models without contacting providers", func(c *capp.Cmd) error {
		if len(c.RemainArgs()) != 0 {
			return fmt.Errorf("usage: suga models [--json]")
		}
		providers, err := appconfig.ReadProviders()
		if err != nil {
			return err
		}
		settings, err := appconfig.ReadModelSettings("")
		if err != nil {
			return err
		}
		catalog := appconfig.BuildModelCatalog(providers, settings)
		catalog.Source = appconfig.ModelCatalogSource()
		if asJSON {
			encoder := json.NewEncoder(stdout)
			encoder.SetIndent("", "  ")
			return encoder.Encode(catalog)
		}
		return writeModelsText(stdout, catalog)
	})
	models.SetOutput(stderr)
	models.BoolVar(&asJSON, "json", false, "write the offline model list as JSON")
	return models
}

func writeModelsText(out io.Writer, catalog appconfig.ModelCatalog) error {
	var b strings.Builder
	// Columns are aligned with a tabwriter: provider ids and model refs vary in
	// length, so fixed padding would misalign them.
	tw := tabwriter.NewWriter(&b, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "Providers (offline)")
	for _, provider := range catalog.Providers {
		if provider.Kind == "preset" && len(provider.ModelIDs) == 0 {
			fmt.Fprintf(tw, "  %s\tpreset; model ids are user-specified\n", provider.ID)
			continue
		}
		fmt.Fprintf(tw, "  %s\t%s; %d configured models", provider.ID, provider.Kind, len(provider.ModelIDs))
		if len(provider.ModelIDs) > 0 {
			fmt.Fprintf(tw, "; model_ids=%s", strings.Join(provider.ModelIDs, ", "))
		}
		fmt.Fprintln(tw)
	}
	_ = tw.Flush()

	b.WriteString("\nModels\n")
	if len(catalog.Models) == 0 {
		b.WriteString("  none\n")
	} else {
		tw = tabwriter.NewWriter(&b, 0, 0, 2, ' ', 0)
		for _, model := range catalog.Models {
			marker := " "
			if containsModelRole(model.Roles, "current") {
				marker = "*"
			}
			role := "configured"
			if len(model.Roles) > 0 {
				role = strings.Join(model.Roles, ",")
			}
			var details []string
			if model.ModelID != "" && model.ModelID != model.Model {
				details = append(details, "model_id="+model.ModelID)
			}
			if model.Type != "" {
				details = append(details, "type="+model.Type)
			}
			details = append(details, fmt.Sprintf("context=%d", model.ContextWindow), "switch="+yesNo(model.CanSwitch))
			if model.ThinkingStyle != "" {
				details = append(details, "thinking_style="+model.ThinkingStyle)
			}
			if model.StreamRequired {
				details = append(details, "stream_required=yes")
			}
			details = append(details, "vision="+yesNo(model.Vision))
			fmt.Fprintf(tw, "  %s %s\t%s\t%s\n", marker, model.Ref, role, strings.Join(details, " "))
		}
		_ = tw.Flush()
	}
	b.WriteString("\n:model switch range: ")
	if len(catalog.Switchable) == 0 {
		b.WriteString("none\n")
	} else {
		b.WriteString(strings.Join(catalog.Switchable, ", "))
		b.WriteByte('\n')
	}
	fmt.Fprintf(&b, "source: %s\nNo network request was made.\n", catalog.Source)
	_, err := io.WriteString(out, b.String())
	return err
}

func containsModelRole(roles []string, want string) bool {
	for _, role := range roles {
		if role == want {
			return true
		}
	}
	return false
}

func yesNo(value bool) string {
	if value {
		return "yes"
	}
	return "no"
}
