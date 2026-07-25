# 命令模板

每个 YAML 文件定义一个可执行模板，通过 `POST /api/v1/configs/templates` 单独导入。模板是唯一允许携带命令文本的受管配置；导入后仍须在 `policies` 的 `allowed_templates` 和 `allowed_tools` 中显式授权才能执行。

```yaml
code: example_check
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

- `code` 必须唯一，规则和诊断计划通过它引用模板。
- `command` 使用 Go 模板语法引用参数；不要拼接未经校验的敏感参数。
- `parameters` 中声明每个参数的必填性与默认值。
- 需要 Worker 本地服务配置时，必须同时声明 `ServiceProfile` 和 `ServiceType` 参数。
- 命令首个可执行程序必须被当前策略的 `allowed_tools` 允许。
