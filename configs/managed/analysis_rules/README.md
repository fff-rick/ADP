# 诊断分析规则

当 LLM 不可用或返回格式错误时，分析器从本目录的规则生成保守诊断结论。通过 `POST /api/v1/configs/analysis_rules` 导入；保存会整体替换当前规则集。

```yaml
id: diagnosis_analyzer
rules:
  - trigger_type: database_unreachable
    fault_type: 数据库不可达
    possible_causes: [网络不通, 服务未启动]
    suggestions: [检查网络和服务状态]
    confidence: 0.6
```

- `trigger_type` 必须与诊断计划的 `trigger_type` 一致。
- `fault_type`、`possible_causes`、`suggestions` 必填。
- `confidence` 取值建议为 0 到 1。该规则是静态降级结论；需要基于实时输出做细粒度推断时，应使用 LLM 分析提示词。
