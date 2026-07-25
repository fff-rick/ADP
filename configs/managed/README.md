# 受管 YAML 配置

本目录保存 ADP 运行时受管配置的源 YAML 文件。

- `templates/`：命令模板定义
- `policies/`：执行策略定义
- `prompts/`：LLM 提示词定义
- `diagnosis_plans/`：诊断计划定义
- `parser_rules/`：任务意图解析规则兜底定义
- `yaml_rules/`：YAML 生成规则兜底定义
- `analysis_rules/`：诊断分析规则兜底定义

可通过 `/api/v1/configs/{kind}` 导入这些文件，由服务端校验、持久化并在运行时生效。

对于空数据库，使用 `--managed-config-dir configs/managed` 启动服务（默认值）会自动导入运行态数据库中尚不存在的源 YAML。启动导入不会覆盖已有运行态配置；需要修改已导入配置时，请使用仅管理员可调用的配置 API。

示例：

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
