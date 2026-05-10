package cmd

import (
	"fmt"
	"os"

	"sandbox/internal/config"
	"github.com/spf13/cobra"
)

var runFlags struct {
	ip       string
	token    string
	language string
	code     string
	file     string
}

var runCmd = &cobra.Command{
	Use:   "run",
	Short: "Run code on a sandbox runner",
	Long: `Submit code to a running sandbox runner and print the output.

Examples:

  # Run an inline snippet
  sandbox run --ip 1.2.3.4 --runner-token <secret> --language python --code 'print("hi")'

  # Run a file
  sandbox run --ip 1.2.3.4 --runner-token <secret> --language python --file script.py`,
	RunE: func(cmd *cobra.Command, args []string) error {
		token := runFlags.token
		if token == "" {
			cfg, err := config.Load()
			if err != nil {
				return fmt.Errorf("loading config: %w", err)
			}
			if r := cfg.Runners[runFlags.ip]; r != nil {
				token = r.Token
			}
			if token == "" {
				return fmt.Errorf("no runner token for %s — pass --runner-token or run 'sandbox server create' with --runner-token to store it", runFlags.ip)
			}
		}
		code, err := readCode(runFlags.code, runFlags.file)
		if err != nil {
			return fmt.Errorf("reading code: %w", err)
		}
		result, err := postRun(cmd.Context(), runFlags.ip, token, runFlags.language, code)
		if err != nil {
			return fmt.Errorf("run failed: %w", err)
		}
		if result.TimedOut {
			return fmt.Errorf("execution timed out after %dms", result.DurationMS)
		}
		fmt.Printf("Output: %s", result.Output)
		fmt.Printf("Duration: %dms\n", result.DurationMS)
		return nil
	},
}

func init() {
	rootCmd.AddCommand(runCmd)
	f := runCmd.Flags()
	f.StringVar(&runFlags.ip, "ip", "", "Runner server IP address")
	f.StringVar(&runFlags.token, "runner-token", "", "Bearer token for the runner API")
	f.StringVar(&runFlags.language, "language", "", "Language to run (python, node, go)")
	f.StringVar(&runFlags.code, "code", "", "Inline code to run")
	f.StringVar(&runFlags.file, "file", "", "Path to a source file to run")
	_ = runCmd.MarkFlagRequired("ip")
	_ = runCmd.MarkFlagRequired("language")
	runCmd.MarkFlagsOneRequired("code", "file")
	runCmd.MarkFlagsMutuallyExclusive("code", "file")
}

// readCode returns the code string from either --code or --file.
// Mutual exclusion and "one required" are enforced by cobra flags.
func readCode(code, file string) (string, error) {
	if code != "" {
		return code, nil
	}
	b, err := os.ReadFile(file)
	if err != nil {
		return "", fmt.Errorf("reading file: %w", err)
	}
	return string(b), nil
}
