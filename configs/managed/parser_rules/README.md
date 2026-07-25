# 任务解析规则配置

本目录的 YAML 用于定义任务解析器的规则兜底能力。解析器本身不内置业务关键词、意图、参数或模板映射；修改或新增支持范围时，应修改本目录中的 YAML，并通过管理 API 导入。

## 生效方式

将 YAML 内容以管理员身份提交到：

`POST /api/v1/configs/parser_rules`

请求体示例：

```json
{
  "yaml_content": "<YAML 文件完整内容>"
}
```

保存成功后，服务会校验正则表达式并立即替换当前规则集，无需重启。查询、更新和删除分别使用：

- `GET /api/v1/configs/parser_rules`
- `PUT /api/v1/configs/parser_rules/{id}`
- `DELETE /api/v1/configs/parser_rules/{id}`

同一套解析能力还需要在 `../prompts/task_parser.yaml` 中维护 LLM 提示词；规则用于 LLM 不可用、超时或返回非结构化结果时的安全兜底。

## 文件结构

```yaml
id: task_parser
name: 规则集名称
rules:
  - pattern: '正则表达式'
    intent: health_check
    target_type: http_service
    risk_level: low
    matched_template: http_health_check
    parameters:
      URL: https://example.com
      Timeout: "10"
```

`id` 是规则集标识；通常使用 `task_parser`。一次保存会**整体替换**当前解析规则，因此文件中应包含希望保留的全部规则。

## 字段规范

- `pattern`：必填，Go RE2 正则表达式，系统自动忽略大小写。请用单引号包裹，避免 YAML 转义问题。
- `intent`：必填，结构化意图名称。建议使用小写英文和下划线，例如 `health_check`。
- `target_type`：必填，目标类型，例如 `http_service`、`mysql`、`nginx`。
- `risk_level`：可选，`low`、`medium` 或 `high`；缺省为 `low`。
- `matched_template`：建议填写。必须是已经存在且已被策略白名单允许的模板代码，例如 `http_health_check`。诊断类多步骤意图可不填。
- `parameters`：可选，字符串键值对，作为该规则命中的默认参数。运行任务时可由请求参数覆盖。

当参数包含 `ServiceProfile` 时，必须同时提供 `ServiceType`（例如 `mysql`、`redis`、`nginx`、`http`）。Worker 据此选择本地服务配置；模板代码不再隐式决定服务类型。

规则按文件内顺序匹配，**第一个命中的规则生效**。请把更具体的规则放在通用规则前面，例如 GitHub 连通性规则应放在通用 HTTP 健康检查规则之前。

## 安全边界

规则配置不能包含 shell 命令，也不能绕过模板和策略白名单。若要新增可执行动作，应先通过 `templates` 配置创建模板，并同步在 `policies` 配置的 `allowed_templates` 中明确允许；之后才在本文件引用该模板。
