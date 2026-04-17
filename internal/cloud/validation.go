package cloud

// ValidationLevel identifies which validation stage produced an error.
type ValidationLevel int

const (
	LevelTemplateParse     ValidationLevel = 1
	LevelTemplateRender    ValidationLevel = 2
	LevelRenderedYAML      ValidationLevel = 3
	LevelConfigSection     ValidationLevel = 4
	LevelResourceStructure ValidationLevel = 5
	LevelVariableReference ValidationLevel = 6
	LevelAgentAccess       ValidationLevel = 7
	LevelRuntimeCompat     ValidationLevel = 8
)

// ValidationError is one structured error produced by a validator.
type ValidationError struct {
	Level   ValidationLevel `json:"level"`
	Field   string          `json:"field,omitempty"`
	Message string          `json:"message"`
	Line    int             `json:"line,omitempty"`
}

func (e ValidationError) Error() string { return e.Message }

// ResourceNode is a typed view of a single rendered Pulumi resource.
type ResourceNode struct {
	Type       string
	Name       string
	Properties map[string]any
	Options    map[string]any
}

// ResourceGraph is the rendered view handed to Provider.Validate for
// runtime cross-field checks.
type ResourceGraph struct {
	Resources map[string]ResourceNode
	Variables map[string]any
	Outputs   map[string]any
}
