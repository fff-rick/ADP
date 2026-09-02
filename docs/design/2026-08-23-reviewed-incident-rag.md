# 审核型故障案例 RAG 设计

## 目标与边界

为 ADP 已审核的历史故障案例提供语义检索，同时维持既有安全边界：LLM
只能通过 `search_incident_cases` 取得脱敏、限长的历史参考；案例不是当前
主机事实，也不能产生或授权任何操作。

不引入独立向量数据库。ADP 已使用 PostgreSQL，采用 pgvector 保持案例、审核
记录与向量的事务关系、备份与访问边界一致。

## 事实来源和索引门禁

只有满足以下条件的案例才可被向量化：

1. `incident_cases.status = approved`；
2. 审核人已确认或修订根因、处置步骤和结果；
3. 参与向量化的文本经过 `model.SanitizeText` 与字段长度限制。

`pending_review`、`rejected`、原始 Worker 输出、凭据和服务 Profile 均不得送往
Embedding Provider。审核通过后的字段变更会改变 `content_hash`，令旧向量失效并
重新进入 `queued`。

## 规范文档

系统在写入或查询向量前按固定顺序构造文本：

```text
症状：{alert_symptoms}
环境：{environment_tags}
证据摘要：{evidence_summary}
已确认根因：{root_cause}
已验证处置：{resolution_steps}
处置结果：{resolution_result}
```

空字段省略，单字段和全文都有限长；`content_hash` 为最终规范文本的 SHA-256。
规范化确保同一审核内容可幂等重试，并能精确判断是否需要重嵌入。

## 数据模型

`incident_case_embeddings` 保存每个案例当前激活的一份向量：案例 ID、文本哈希、
模型名、维度、向量、状态、尝试次数和最后错误。当前固定 1024 维，避免同一 HNSW
索引混用不同维度或模型空间。切换模型必须先完成全量重嵌入，再将新模型切为查询模型。

状态机：`queued -> ready`；可恢复失败为 `queued -> failed -> queued`。`approved`
撤销或内容哈希变更会从检索候选中排除旧向量。

## 检索

先强制 `approved`、环境标签等结构化过滤；再执行：

1. 现有关键词/错误码匹配；
2. 当前 embedding 模型下的余弦近邻召回；
3. 第一阶段以“关键词结果优先 + 去重后的语义补充”融合，最多返回 5 条；积累
   评测集后再引入有分数归一化校验的 RRF。

词法检索保留精确错误码和中文短语的优势；语义召回解决同义告警表达。Embedding
不可用、任务积压或向量尚未就绪时，服务无损降级为现有结构化/关键词检索。

## 运行与验收

Embedding 任务由 Server 的持久化轮询器限流处理；每次调用设独立短超时，不阻塞
审核 API。记录延迟、失败数、队列深度和召回来源。上线前必须验证：待审核/拒绝案例
不会被查询；敏感值不离开 ADP；字段更新会重排队；相同内容不重复调用；关闭 RAG 时
查询结果与当前版本一致。
