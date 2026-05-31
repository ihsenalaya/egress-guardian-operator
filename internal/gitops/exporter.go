package gitops

import (
	"fmt"
	"os"
	"path/filepath"
)

// ExportPolicy writes policyYAML to outputPath/<name>.yaml.
// The directory is created if it does not exist.
func ExportPolicy(outputPath, name, policyYAML string) error {
	if err := os.MkdirAll(outputPath, 0o755); err != nil {
		return fmt.Errorf("create output dir %s: %w", outputPath, err)
	}
	dest := filepath.Join(outputPath, name+".yaml")
	return os.WriteFile(dest, []byte(policyYAML), 0o644)
}
