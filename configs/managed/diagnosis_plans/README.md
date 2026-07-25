# 诊断计划配置

本目录定义可重复使用的多步骤诊断计划。计划通过 `POST /api/v1/configs/diagnosis_plans` 导入；保存成功后立即生效，无需重启服务。

每份文件必须包含 `trigger_type`、`title`、`keywords` 和至少一个 `steps`。系统按 `keywords` 与用户描述做不区分大小写的包含匹配；先匹配到的计划被采用，因此应避免不同计划使用过于宽泛的关键词。

```yaml
trigger_type: database_unreachable
title: 数据库不可达诊断
keywords: [数据库连接失败, mysql 无法连接]
steps:
  - step_no: 1
    name: 检查端口
    description: 确认数据库端口是否监听。
    template_code: check_port
    parameters:
      ServiceProfile: mysql_prod
    timeout_seconds: 15
```

- `trigger_type`：计划的稳定唯一标识，只使用小写英文、数字和下划线。
- `keywords`：触发该计划的关键词列表；至少提供一个具有区分度的关键词。
- `template_code`：必须引用已导入且已被策略允许的模板。
- `parameters`：任务默认参数；敏感信息必须通过 Worker 本地 `ServiceProfile` 引用，不能写明文密码或令牌。
- 参数使用 `ServiceProfile` 时，必须同时声明 `ServiceType`；Worker 仅按该声明查找对应类型的本地服务配置。
- `timeout_seconds`：单步超时秒数；不填时由执行端使用默认超时。

计划配置只描述流程，不能直接嵌入 shell 命令。新执行能力应先在 `templates` 中创建模板、在 `policies` 中加入白名单，再在本目录引用。
