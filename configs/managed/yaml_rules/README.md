# YAML 生成规则

当 LLM 未配置、调用失败或输出不合法 YAML 时，系统使用本目录的规则生成受限任务 YAML。通过 `POST /api/v1/configs/yaml_rules` 导入，保存后立即替换当前规则集。

```yaml
id: yaml_generator
name: 规则集名称
rules:
  - keywords: [关键词一, keyword-two]
    name: 生成任务名称
    worker_type: shell
    workers: [all]
    tasks:
      - name: 步骤名称
        template: 已批准的模板代码
        parameters:
          ServiceProfile: worker_local_profile
```

- `keywords`、`name`、`tasks` 必填；规则按顺序匹配，任一关键词命中即采用该规则。
- `template` 必须已在模板配置中存在，并在策略的 `allowed_templates` 中允许。
- 参数只能使用非敏感默认值或 Worker 本地 `ServiceProfile`；禁止明文密码、Token 和连接串。
- 使用 `ServiceProfile` 时必须同时提供 `ServiceType`，例如 `ServiceType: redis`。
- 没有规则匹配时会明确返回错误，不会生成任意默认任务。
