package builtin

import "adp/internal/module"

// NewRegistry creates an empty module registry. Modules are registered at
// runtime from managed YAML template configurations.
func NewRegistry() *module.Registry {
	return module.NewRegistry()
}

// ClearTemplateModules removes all modules from the registry. Call before
// re-registering from managed configs to avoid stale entries.
func ClearTemplateModules(reg *module.Registry) {
	reg.Clear()
}
