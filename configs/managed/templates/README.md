# 命令模板

按 Worker 类型管理模板：一个 YAML 文件包含该类型的全部模板，例如 `mysql.yaml`、`redis.yaml` 和 `shell.yaml`。模板是唯一允许携带命令文本的受管配置；导入后仍须在 `policies` 的 `allowed_templates` 和 `allowed_tools` 中显式授权才能执行。

```yaml
id: shell
name: Shell templates
templates:
  - code: example_check
    name: 示例检查
    description: 可选说明
    tool_type: shell
    command: curl -sI {{.URL}}
    risk_level: low
    parameters:
      - name: URL
        description: 检查地址
        required: true
```

- `id` 是该模板组的唯一标识；`code` 必须在全部模板中唯一，规则和诊断计划通过它引用模板。
- 一个文件中的每个模板应使用相同的 `tool_type`，与文件名及 Worker 类型一致。
- `command` 使用 Go 模板语法引用参数；不要拼接未经校验的敏感参数。
- `parameters` 中声明每个参数的必填性与默认值。
- 需要 Worker 本地服务配置时，必须同时声明 `ServiceProfile` 和 `ServiceType` 参数。
- 命令首个可执行程序必须被当前策略的 `allowed_tools` 允许。

旧的“一个文件一个模板”格式仍支持导入，便于已有配置迁移；新配置应使用上述分组格式。
