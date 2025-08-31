# 📊 sqvect 文本相似度 Score 详解

## 🎯 Score 范围和含义

- **标准范围**: 0.0 到 1.0
- **扩展范围**: 可以通过 `AllowScoreAboveOne=true` 允许超过 1.0
- **含义**:
  - `0.0`: 完全不匹配
  - `1.0`: 完美匹配
  - `> 1.0`: 带有 boost 权重的强匹配

## ⚙️ 配置选项

### TextSimilarityOptions

```go
type TextSimilarityOptions struct {
    BoostTerms         map[string]float64   // 特殊词汇的权重倍数
    TermPairs          map[string][]string  // 跨语言词汇对应关系
    AllowScoreAboveOne bool                 // 是否允许分数超过1.0
}
```

## 🚀 Boost 功能详解

### 工作原理

1. **基础相似度计算**: 使用多种算法计算文本相似度 (0.0-1.0)
2. **Boost 权重应用**: 如果查询和内容包含配置的特殊词汇，应用权重倍数
3. **跨语言支持**: 通过 `TermPairs` 支持中英文对应词汇的 boost

### 示例

```go
// 配置高权重
options := sqvect.TextSimilarityOptions{
    AllowScoreAboveOne: true,
    BoostTerms: map[string]float64{
        "yinshu": 3.0,  // 3倍权重
        "音书":    3.0,
    },
    TermPairs: map[string][]string{
        "yinshu": {"音书"},  // 建立对应关系
        "音书":    {"yinshu"},
    },
}

sim := sqvect.NewTextSimilarityWithOptions(options)

// 结果
score := sim.CalculateSimilarity("yinshu", "音书")  // 3.0 (超过1.0!)
```

## 📈 实际测试结果

### 基础匹配 (无 boost)

```
'yinshu' vs '音书': 1.000
'beijing' vs '北京': 1.000
'coffee' vs '咖啡': 0.333 (需要配置TermPairs才能完美匹配)
```

### 带 boost 的匹配

```
# AllowScoreAboveOne = true, boost = 3.0
'yinshu' vs '音书': 3.000 🔥
'beijing yinshu' vs '北京音书': 15.000 🔥 (多个boost词相乘)
```

### 限制 vs 不限制对比

```
# boost = 5.0
AllowScoreAboveOne = false: 1.000 (被限制)
AllowScoreAboveOne = true:  5.000 (真实boost效果)
```

## 🎨 使用场景

### 1. 标准使用 (兼容模式)

```go
sim := sqvect.NewTextSimilarity()  // 默认配置
// 分数范围: 0.0-1.0
```

### 2. 中文支持

```go
sim := sqvect.NewTextSimilarityWithOptions(sqvect.DefaultChineseOptions())
// 常用中英文词汇自动支持
```

### 3. 自定义权重

```go
options := sqvect.TextSimilarityOptions{
    AllowScoreAboveOne: true,
    BoostTerms: map[string]float64{
        "重要词汇": 5.0,  // 5倍权重
    },
}
```

## 🔧 配置建议

### 权重设置

- **1.0**: 标准权重，不改变分数
- **1.5-2.0**: 轻微提升
- **3.0-5.0**: 显著提升
- **10.0+**: 极高权重 (谨慎使用)

### 向后兼容

- `AllowScoreAboveOne = false`: 保持传统 0-1 范围
- `AllowScoreAboveOne = true`: 启用 boost 超过 1.0 的能力

## 🎯 核心优势

1. **灵活性**: 可配置的 boost 和词汇对应
2. **跨语言**: 中英文自动匹配支持
3. **兼容性**: 默认行为保持不变
4. **精确控制**: 细粒度的权重配置
5. **业务适配**: 可根据具体场景调整匹配策略

## 💡 最佳实践

1. **生产环境**: 通常使用 `AllowScoreAboveOne = false` 保持分数可预测
2. **排序场景**: 使用 `AllowScoreAboveOne = true` 让重要词汇排名更高
3. **中英文混合**: 使用 `DefaultChineseOptions()` 作为起点
4. **业务词汇**: 根据具体业务添加专门的 `BoostTerms` 和 `TermPairs`