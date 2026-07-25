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

对于空数据库，使用 `--managed-config-dir configs/managed` 启动服务（默认值）会自动导入运行态数据库中尚不存在的源 YAML。

- `--managed-config-sync-mode missing`：默认模式，只导入缺失配置；发现源文件与运行态不同会保留运行态配置。
- `--managed-config-sync-mode enforce`：GitOps 模式，启动时以仓库 YAML 为准，覆盖同 ID 的运行态配置。

管理员可调用 `GET /api/v1/configs/sync` 查看导入、同步和漂移情况；调用 `POST /api/v1/configs/sync?enforce=true` 可立即按 GitOps 源文件强制同步。上述同步操作都会记录审计日志。

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
