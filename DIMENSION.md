# 向量维度自动适配功能

## 概述

SqVect 支持自动处理向量维度不匹配问题。当向量维度发生变化时（如从 768 维切换到 1536 维），库会自动适配，无需手动干预。

## 使用场景

### 常见维度变化场景
- **模型升级**: OpenAI text-embedding-ada-002 (1536维) → text-embedding-3-large (3072维)  
- **模型切换**: Sentence-BERT (768维) → OpenAI (1536维)
- **多模型混用**: 不同来源的向量需要存储在同一个数据库中

### 自动处理的情况
1. **首次插入**: 自动检测并设置向量维度
2. **维度不匹配**: 自动适配新向量到现有维度
3. **查询适配**: 自动适配查询向量维度
4. **批量插入**: 智能处理混合维度数据

## 适配策略

### 默认策略: 智能适配 (Smart Adapt)

```go
// 无需任何配置，自动处理
store, err := sqvect.New("vectors.db", 0) // 0 = 自动检测维度
```

**策略逻辑**:
1. **降维** (1536→768): 截断前 768 维 + 重新归一化
2. **升维** (768→1536): 原向量 + 随机小噪声填充
3. **智能迁移**: 当新维度占比超过 80% 时，触发存储维度升级

### 可选策略配置

```go
config := sqvect.Config{
    Path: "vectors.db", 
    AutoDimAdapt: sqvect.AutoTruncate,    // 总是截断到较小维度
    // 或
    AutoDimAdapt: sqvect.AutoPad,         // 总是填充到较大维度  
    // 或
    AutoDimAdapt: sqvect.WarnOnly,        // 只警告，不自动适配
}
```

## 使用示例

### 基本使用
```go
package main

import (
    "context"
    "log"
    "github.com/liliang-cn/sqvect"
)

func main() {
    // 创建store，维度自动检测
    store, err := sqvect.New("mixed_dimensions.db", 0)
    if err != nil {
        log.Fatal(err)
    }
    defer store.Close()
    
    ctx := context.Background()
    
    // 插入 768 维向量
    err = store.Upsert(ctx, &sqvect.Embedding{
        ID:      "bert_vector",
        Vector:  make([]float32, 768),  // BERT embedding
        Content: "BERT encoded text",
    })
    
    // 插入 1536 维向量 - 自动适配！
    err = store.Upsert(ctx, &sqvect.Embedding{
        ID:      "openai_vector", 
        Vector:  make([]float32, 1536), // OpenAI embedding
        Content: "OpenAI encoded text",
    })
    
    // 使用 3072 维查询 - 自动适配！
    query := make([]float32, 3072) // GPT-4 embedding
    results, err := store.Search(ctx, query, sqvect.SearchOptions{TopK: 5})
    if err != nil {
        log.Fatal(err)
    }
    
    log.Printf("Found %d results", len(results))
}
```

### 批量处理不同维度
```go
func insertMixedDimensions() {
    store, _ := sqvect.New("vectors.db", 0)
    defer store.Close()
    
    embeddings := []*sqvect.Embedding{
        {ID: "doc1", Vector: make([]float32, 768)},   // BERT
        {ID: "doc2", Vector: make([]float32, 1536)},  // OpenAI  
        {ID: "doc3", Vector: make([]float32, 384)},   // MiniLM
        {ID: "doc4", Vector: make([]float32, 3072)},  // Large model
    }
    
    // 所有维度都会自动适配到主流维度
    err := store.UpsertBatch(context.Background(), embeddings)
    if err != nil {
        log.Fatal(err)
    }
}
```

## 适配过程详解

### 1. 维度检测
库会分析数据库中现有向量的维度分布：
- 统计每种维度的向量数量
- 确定主流维度（数量最多的维度）
- 计算迁移优先级

### 2. 适配算法

**截断适配** (高维→低维):
```
原向量: [1.2, 0.8, -0.3, 0.5, 0.1, ...] (1536维)
截断后: [1.2, 0.8, -0.3, 0.5, 0.1, ...] (768维)  
重新归一化: normalize([1.2, 0.8, -0.3, ...])
```

**填充适配** (低维→高维):
```
原向量: [1.2, 0.8, -0.3] (3维)
填充后: [1.2, 0.8, -0.3, 0.001, -0.002, ...] (5维)
填充值: 原向量标准差的 1% 作为随机噪声
```

### 3. 性能影响
- **内存开销**: 几乎无额外开销
- **计算开销**: 截断 O(1)，填充 O(n)
- **存储开销**: 适配后的向量正常存储

## 日志输出

启用详细日志查看适配过程：

```go
config := sqvect.Config{
    Path: "vectors.db",
    VerboseLogging: true,  // 启用详细日志
}
```

日志示例：
```
[INFO] Auto-detected vector dimension: 768
[INFO] Dimension adaptation: 1536 → 768 (truncate strategy)  
[INFO] Dimension upgrade triggered: 768 → 1536 (80% vectors are 1536-dim)
[WARN] Mixed dimensions detected: 768(60%), 1536(40%)
```

## 注意事项

### 精度损失
- **截断**: 丢失高维信息，可能影响相似性计算
- **填充**: 增加噪声维度，影响相对较小
- **建议**: 尽量使用相同维度的模型，或使用重新编码

### 性能考虑
- 首次适配有轻微计算开销
- 后续相同维度的向量无额外开销
- HNSW 索引在维度变化时会重建

### 最佳实践
1. **模型迁移**: 考虑重新编码所有文本而非向量适配
2. **维度选择**: 选择一个目标维度后保持一致
3. **测试验证**: 适配后验证搜索质量是否满足需求

## 故障排查

### 常见问题

**Q: 适配后搜索结果质量下降？**
A: 截断会丢失信息。建议使用相同维度模型或重新编码原文本。

**Q: 内存使用突然增加？**  
A: 可能触发了维度升级。检查日志确认升级过程。

**Q: 如何禁用自动适配？**
A: 设置 `AutoDimAdapt: sqvect.WarnOnly`

### 调试信息
```go
// 查看当前维度分布
stats, _ := store.GetDimensionStats(context.Background())
fmt.Printf("Primary dimension: %d (%d vectors)\n", 
    stats.PrimaryDimension, stats.PrimaryCount)
    
for dim, count := range stats.Dimensions {
    fmt.Printf("Dimension %d: %d vectors\n", dim, count)
}
```

## 版本兼容性

- **向前兼容**: 现有数据库自动支持维度适配
- **向后兼容**: 可通过配置禁用自动适配
- **升级平滑**: 无需数据迁移，即开即用

## 配置参考

```go
type Config struct {
    Path         string        `json:"path"`         // 数据库路径
    VectorDim    int          `json:"vectorDim"`    // 0=自动检测
    AutoDimAdapt AdaptPolicy  `json:"autoDimAdapt"` // 适配策略
}

type AdaptPolicy int
const (
    SmartAdapt   AdaptPolicy = iota // 智能适配（默认）
    AutoTruncate                    // 总是截断
    AutoPad                         // 总是填充  
    WarnOnly                        // 仅警告
)
```

简单配置即可享受自动维度适配功能！