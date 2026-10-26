package cli

import (
	"fmt"
	"io"
	"os"

	"github.com/gookit/goutil/cflag/capp"
	appconfig "github.com/inhere/suga/internal/config"
)

// newInitCommand builds the explicit bootstrap entry point. The startup gate
// handles all other commands before configuration loading; init owns its own
// call so it also works when no config exists yet.
func newInitCommand(stdout, stderr io.Writer) *capp.Cmd {
	var force bool
	cmd := capp.NewCmd("init", "Initialize the private suga home and onboarding templates", func(c *capp.Cmd) error {
		if len(c.RemainArgs()) != 0 {
			return fmt.Errorf("usage: suga init [--force]")
		}
		result, err := appconfig.EnsureHome(appconfig.BootstrapOptions{Force: force})
		if err != nil {
			return err
		}
		if err := writeInitResult(stdout, result); err != nil {
			return err
		}
		return appconfig.WriteBootstrapNotice(stderr, result)
	})
	cmd.SetOutput(stderr)
	cmd.BoolVar(&force, "force", false, "backup and replace the home config template")
	return cmd
}

func writeInitResult(out io.Writer, result appconfig.BootstrapResult) error {
	tr := cliTranslator()
	if result.Source == "explicit" {
		_, err := fmt.Fprintf(out, "%s\n", cliText(tr, "cli.init.explicit", map[string]any{"path": os.Getenv("SUGA_CONFIG")}))
		return err
	}
	if result.Backup != "" {
		if _, err := fmt.Fprintf(out, "%s\n", cliText(tr, "cli.init.backup", map[string]any{"config": result.Config, "backup": result.Backup})); err != nil {
			return err
		}
	}
	if result.CreatedConfig {
		_, err := fmt.Fprintf(out, "%s\n", cliText(tr, "cli.init.written", map[string]any{"config": result.Config}))
		return err
	}
	_, err := fmt.Fprintf(out, "%s\n", cliText(tr, "cli.init.exists", map[string]any{"config": result.Config}))
	return err
}
