# Managed YAML Configs

This directory stores source YAML files for runtime-managed ADP configs.

- `templates/`: command template definitions
- `policies/`: execution policy definitions
- `prompts/`: LLM prompt definitions
- `diagnosis_plans/`: diagnosis plan definitions
- `parser_rules/`: rule-based task intent fallback definitions
- `yaml_rules/`: YAML generation fallback definitions
- `analysis_rules/`: diagnosis-analysis fallback definitions

Load these files through `/api/v1/configs/{kind}` so the server can validate,
persist, and apply them at runtime.

Examples:

- `templates/disk_usage_check.yaml` -> `/api/v1/configs/templates`
- `policies/default.yaml` -> `/api/v1/configs/policies`
- `prompts/yaml_generator.yaml` -> `/api/v1/configs/prompts`
- `diagnosis_plans/http_unreachable.yaml` -> `/api/v1/configs/diagnosis_plans`
- `prompts/task_parser.yaml` -> `/api/v1/configs/prompts`
- `prompts/diagnosis_planner.yaml` -> `/api/v1/configs/prompts`
- `prompts/diagnosis_analyzer.yaml` -> `/api/v1/configs/prompts`
- `parser_rules/task_parser.yaml` -> `/api/v1/configs/parser_rules`
- `yaml_rules/default.yaml` -> `/api/v1/configs/yaml_rules`
- `analysis_rules/default.yaml` -> `/api/v1/configs/analysis_rules`
