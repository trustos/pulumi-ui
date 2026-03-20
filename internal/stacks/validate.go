package stacks

import (
	"fmt"

	"github.com/trustos/pulumi-ui/internal/programs"
)

// ValidateAgainstProgram checks that all required config fields are present.
func ValidateAgainstProgram(cfg map[string]string, fields []programs.ConfigField) error {
	for _, f := range fields {
		v, ok := cfg[f.Key]
		if f.Required && (!ok || v == "") {
			return fmt.Errorf("required field %q is missing", f.Key)
		}
	}
	return nil
}
