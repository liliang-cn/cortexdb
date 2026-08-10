# Palantir 式本体论 (Ontology v2) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 把 CortexDB 现有的 ontology-lite（entity types + relation types）替换成一套 Palantir Foundry 风格的本体论：带数据类型的对象类型、强制主键、每侧独立基数的链接类型、接口多态、可组合的 ObjectSet 代数、带审计的 Action 写入路径，以及由 schema 生成的类型化 MCP 工具。

**Architecture:** 全部落在 `pkg/cortexdb` 门面层，沿用该包"扁平 + 主题前缀文件名"的既有约定。类型系统和存储走**全新表 `ontology_schemas_v2`**，旧表 `graph_ontology_schemas` 不读不写不删。ObjectSet 是一个递归 union 类型，编译期下推到三个**已有**引擎——`core` 的向量索引、FTS5，和 `graph` 的遍历——而不是新建检索引擎。Action 是一条与通用 `upsert_entities` 平行的写入路径，由 schema 上的 `strict_actions` 开关决定是否独占。

**Tech Stack:** Go 1.25、`modernc.org/sqlite`（纯 Go，无 CGO）、SQLite FTS5、`github.com/modelcontextprotocol/go-sdk/mcp`。无新增第三方依赖。

---

## 关键设计决定（已拍板，实现时不要再动摇）

| 决定 | 取值 | 理由 |
|---|---|---|
| 旧数据迁移 | **建新表 `ontology_schemas_v2`**，旧表不读不写 | 零迁移风险；旧 schema 记录静默失效是可接受代价 |
| 主键 | **每个 ObjectType 必须声明 `primary_key`**，否则 schema 校验失败 | 实体身份模型干净；解决现在靠 name 去重的重复问题 |
| Action 治理强度 | schema 上 `strict_actions bool`，**默认 false** | 默认不破坏现有 `upsert_entities` 用法；打开后得到强治理 |
| 链接基数 | **每侧各自 `ONE`/`MANY`**（照抄 `LinkTypeSideV2`） | 比单一 `one_to_many` 枚举更精确；一对多 = ONE + MANY |
| API 名大小写 | **保留原样存储**，查找时大小写不敏感 | 现有 `normalizeOntologyLabel` 全小写导致 `Airport`/`airport` 不可区分 |
| 不做 | 分支/proposal、Functions 运行时、行级安全、backing datasource | 嵌入式单文件库的边界；违反"不引 SDK"的红线 |

### 术语到现有代码的映射

- `OntologyObjectType.APIName` ↔ 图里 `GraphNode.NodeType`
- `OntologyLinkType.APIName` ↔ 图里 `GraphEdge.EdgeType`（**不是**侧的 api name）
- `OntologyLinkSide.APIName` 只用于遍历时的可读标签（`expand_graph` 输出）
- 实体属性来自 `ToolEntityInput.Metadata`（`map[string]string`）
- 关系属性来自 `ToolRelationInput.Metadata`

---

## File Structure

### 新建

| 文件 | 职责 |
|---|---|
| `pkg/cortexdb/ontology_types.go` | **重写**。v2 全部类型定义：Schema / ObjectType / Property / DataType / LinkType / LinkSide / InterfaceType / ActionType / ObjectSet。纯数据，无逻辑 |
| `pkg/cortexdb/ontology_normalize.go` | api name 校验与大小写不敏感索引；属性值按 DataType 解析 |
| `pkg/cortexdb/ontology_compile.go` | `compiledOntology`：从 Schema 建查找表（对象类型、链接类型、接口闭包），供所有校验路径共用 |
| `pkg/cortexdb/ontology_validation.go` | **重写**。schema 自身合法性校验 + 写入准入校验（类型、必填、主键、基数） |
| `pkg/cortexdb/ontology_storage.go` | **重写**。新表 `ontology_schemas_v2` 的 CRUD |
| `pkg/cortexdb/ontology_identity.go` | 主键 → 节点 ID 推导 |
| `pkg/cortexdb/ontology_interface.go` | 接口继承闭包展开、implements 映射校验、类型闭包查询 |
| `pkg/cortexdb/objectset_types.go` | ObjectSet 递归代数与 Predicate 类型 |
| `pkg/cortexdb/objectset_resolve.go` | ObjectSet → 节点 ID 集合；下推到向量 / FTS5 / 图遍历 |
| `pkg/cortexdb/ontology_action_types.go` | ActionType / 参数 / 规则 / 提交条件的类型定义 |
| `pkg/cortexdb/ontology_action_apply.go` | Action 执行：validate-only、return-edits、审计落库 |
| `pkg/cortexdb/ontology_sdk.go` | Schema → 类型化 MCP ToolDefinition 生成 |
| `pkg/cortexdb/ontology_diff.go` | 两个 schema 版本间的差异与破坏性变更检测 |

### 修改

| 文件 | 改动 |
|---|---|
| `pkg/cortexdb/ontology_api.go` | 请求/响应类型换成 v2；新增 objectset / action / diff 的 API |
| `pkg/cortexdb/ontology_tooldefs.go` | 工具定义换成 v2 入参 schema；新增工具 |
| `pkg/cortexdb/ontology_dispatch.go` | 新工具的 dispatch 分支 |
| `pkg/cortexdb/ontology_mcp.go` | 新工具的 MCP 注册（`mcp_tool_coverage_test.go` 会强制要求） |
| `pkg/cortexdb/graphrag_tool_ingest.go:152,296` | 校验调用点改用 v2 校验器；接入主键身份 |
| `pkg/cortexdb/knowledge_tx.go:102,370` | 同上 |
| `pkg/cortexdb/graphrag.go:294` | 同上 |
| `pkg/cortexdb/inference_runtime.go:129` | 同上 |
| `pkg/cortexdb/cortexdb.go:25-26` | `ontologySchemaInit` 改指向新表 |

### 测试

每个新文件配同名 `_test.go`。测试统一用仓库既有模式建库：

```go
dbPath := fmt.Sprintf("test_ontology_v2_%d.db", time.Now().UnixNano())
db, err := Open(DefaultConfig(dbPath))
```

---

## 分期

| 期 | 内容 | 产出 |
|---|---|---|
| **1** | 类型系统 + 校验 + 存储 + 工具面 | 能存取 v2 schema，schema 自身合法性有保障 |
| **2** | 接入写入路径：主键身份、类型校验、基数校验 | v2 schema 真正开始约束图写入 |
| **3** | Interfaces 多态 | 按接口检索能召回所有实现类型 |
| **4** | ObjectSet 代数 | 向量 / 全文 / 图遍历统一到一个可组合表达式 |
| **5** | Action types | 带审计、dry-run、edit-diff 的治理写入路径 |
| **6** | 类型化工具生成 + schema diff + 发布 | schema 直接变成 agent 可调的类型化工具 |

每期独立 commit，跑 `go test -race ./pkg/cortexdb` 通过后再进下一期。

---

# 第 1 期：类型系统 + 校验 + 存储

### Task 1: v2 类型定义

**Files:**
- Create: `pkg/cortexdb/ontology_types.go`（**先删除**旧文件全部内容再写）
- Test: `pkg/cortexdb/ontology_types_test.go`

- [ ] **Step 1: 写失败的测试**

```go
package cortexdb

import (
	"encoding/json"
	"testing"
)

func TestOntologySchemaJSONRoundTrip(t *testing.T) {
	schema := OntologySchema{
		SchemaID: "aviation",
		Name:     "Aviation",
		Version:  1,
		Active:   true,
		ObjectTypes: []OntologyObjectType{
			{
				APIName:           "Airport",
				DisplayName:       "Airport",
				PluralDisplayName: "Airports",
				Status:            OntologyStatusActive,
				Visibility:        OntologyVisibilityNormal,
				PrimaryKey:        "iataCode",
				TitleProperty:     "airportName",
				Properties: []OntologyProperty{
					{APIName: "iataCode", DataType: OntologyDataType{Kind: OntologyDataString}, Required: true},
					{APIName: "airportName", DataType: OntologyDataType{Kind: OntologyDataString}, Required: true, Searchable: true},
					{APIName: "elevation", DataType: OntologyDataType{Kind: OntologyDataInteger}},
					{APIName: "position", DataType: OntologyDataType{Kind: OntologyDataGeoPoint}},
					{APIName: "embedding", DataType: OntologyDataType{Kind: OntologyDataVector, Dimension: 768}},
				},
			},
		},
		LinkTypes: []OntologyLinkType{
			{
				APIName: "flightDeparture",
				A:       OntologyLinkSide{APIName: "departures", ObjectTypeAPIName: "Airport", Cardinality: OntologyCardinalityMany},
				B:       OntologyLinkSide{APIName: "origin", ObjectTypeAPIName: "Flight", Cardinality: OntologyCardinalityOne, ForeignKeyProperty: "originIata"},
			},
		},
	}

	encoded, err := json.Marshal(schema)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded OntologySchema
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if decoded.ObjectTypes[0].PrimaryKey != "iataCode" {
		t.Fatalf("primary key lost: %q", decoded.ObjectTypes[0].PrimaryKey)
	}
	if decoded.ObjectTypes[0].Properties[4].DataType.Dimension != 768 {
		t.Fatalf("vector dimension lost: %d", decoded.ObjectTypes[0].Properties[4].DataType.Dimension)
	}
	if decoded.LinkTypes[0].B.Cardinality != OntologyCardinalityOne {
		t.Fatalf("cardinality lost: %q", decoded.LinkTypes[0].B.Cardinality)
	}
	if decoded.LinkTypes[0].B.ForeignKeyProperty != "originIata" {
		t.Fatalf("foreign key lost: %q", decoded.LinkTypes[0].B.ForeignKeyProperty)
	}
}

func TestOntologyDataTypeNestedKinds(t *testing.T) {
	dt := OntologyDataType{
		Kind:     OntologyDataArray,
		ItemType: &OntologyDataType{Kind: OntologyDataString},
	}
	encoded, err := json.Marshal(dt)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded OntologyDataType
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded.ItemType == nil || decoded.ItemType.Kind != OntologyDataString {
		t.Fatalf("item type lost: %+v", decoded.ItemType)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./pkg/cortexdb -run 'TestOntologySchemaJSONRoundTrip|TestOntologyDataTypeNestedKinds' -v`
Expected: 编译失败，`undefined: OntologyObjectType`、`undefined: OntologyStatusActive` 等。

- [ ] **Step 3: 写实现**

清空 `pkg/cortexdb/ontology_types.go` 并写入：

```go
package cortexdb

import "time"

// OntologyStatus is the lifecycle stage of a type, mirroring Foundry's release status.
type OntologyStatus string

const (
	OntologyStatusActive       OntologyStatus = "active"
	OntologyStatusExperimental OntologyStatus = "experimental"
	OntologyStatusDeprecated   OntologyStatus = "deprecated"
)

// OntologyVisibility controls how prominently a type surfaces to callers.
type OntologyVisibility string

const (
	OntologyVisibilityNormal    OntologyVisibility = "normal"
	OntologyVisibilityProminent OntologyVisibility = "prominent"
	OntologyVisibilityHidden    OntologyVisibility = "hidden"
)

// OntologyDataKind enumerates the property base types CortexDB supports.
// This is Foundry's discriminator list minus the types that need a Foundry
// backend (attachment, mediaReference, timeseries, geotimeSeriesReference).
type OntologyDataKind string

const (
	OntologyDataString    OntologyDataKind = "string"
	OntologyDataInteger   OntologyDataKind = "integer"
	OntologyDataLong      OntologyDataKind = "long"
	OntologyDataDouble    OntologyDataKind = "double"
	OntologyDataDecimal   OntologyDataKind = "decimal"
	OntologyDataBoolean   OntologyDataKind = "boolean"
	OntologyDataDate      OntologyDataKind = "date"
	OntologyDataTimestamp OntologyDataKind = "timestamp"
	OntologyDataGeoPoint  OntologyDataKind = "geopoint"
	OntologyDataGeoShape  OntologyDataKind = "geoshape"
	OntologyDataVector    OntologyDataKind = "vector"
	OntologyDataArray     OntologyDataKind = "array"
	OntologyDataStruct    OntologyDataKind = "struct"
	OntologyDataMarking   OntologyDataKind = "marking"
)

// OntologyCardinality is the multiplicity of one side of a link type.
type OntologyCardinality string

const (
	OntologyCardinalityOne  OntologyCardinality = "ONE"
	OntologyCardinalityMany OntologyCardinality = "MANY"
)

// OntologyDataType describes a property's base type, including the nested
// element type for arrays, the field list for structs, and the dimension for
// vectors.
type OntologyDataType struct {
	Kind      OntologyDataKind   `json:"kind"`
	ItemType  *OntologyDataType  `json:"item_type,omitempty"`
	Fields    []OntologyProperty `json:"fields,omitempty"`
	Dimension int                `json:"dimension,omitempty"`
}

// OntologyProperty is a typed characteristic of an object type or interface.
type OntologyProperty struct {
	APIName     string           `json:"api_name"`
	DisplayName string           `json:"display_name,omitempty"`
	Description string           `json:"description,omitempty"`
	DataType    OntologyDataType `json:"data_type"`
	Required    bool             `json:"required,omitempty"`
	// Searchable routes the property value into FTS5 so ObjectSet text
	// predicates can match on it.
	Searchable bool `json:"searchable,omitempty"`
	// Vectorized routes the property value into the vector index so
	// ObjectSet nearest-neighbour predicates can match on it.
	Vectorized bool `json:"vectorized,omitempty"`
}

// OntologyObjectType is the schema definition of a real-world entity.
// PrimaryKey is mandatory: it is what gives objects a stable identity.
type OntologyObjectType struct {
	APIName           string             `json:"api_name"`
	DisplayName       string             `json:"display_name,omitempty"`
	PluralDisplayName string             `json:"plural_display_name,omitempty"`
	Description       string             `json:"description,omitempty"`
	Status            OntologyStatus     `json:"status,omitempty"`
	Visibility        OntologyVisibility `json:"visibility,omitempty"`
	Icon              string             `json:"icon,omitempty"`
	PrimaryKey        string             `json:"primary_key"`
	TitleProperty     string             `json:"title_property,omitempty"`
	Properties        []OntologyProperty `json:"properties,omitempty"`
	Implements        []string           `json:"implements,omitempty"`
	Aliases           []string           `json:"aliases,omitempty"`
}

// OntologyLinkSide is one end of a link type. Foundry models multiplicity
// per side rather than as a single one-to-many enum: a one-to-many link is
// one side ONE and one side MANY.
type OntologyLinkSide struct {
	APIName            string              `json:"api_name"`
	DisplayName        string              `json:"display_name,omitempty"`
	ObjectTypeAPIName  string              `json:"object_type_api_name"`
	Cardinality        OntologyCardinality `json:"cardinality"`
	ForeignKeyProperty string              `json:"foreign_key_property,omitempty"`
}

// OntologyLinkType is a bidirectional relationship between two object types.
// Graph edges carry the link type APIName as their EdgeType; the side API
// names are traversal labels only.
type OntologyLinkType struct {
	APIName     string           `json:"api_name"`
	Description string           `json:"description,omitempty"`
	Status      OntologyStatus   `json:"status,omitempty"`
	A           OntologyLinkSide `json:"a"`
	B           OntologyLinkSide `json:"b"`
}

// OntologyInterfaceType is an abstract shape that object types implement,
// giving polymorphic retrieval across unrelated concrete types.
type OntologyInterfaceType struct {
	APIName     string             `json:"api_name"`
	DisplayName string             `json:"display_name,omitempty"`
	Description string             `json:"description,omitempty"`
	Extends     []string           `json:"extends,omitempty"`
	Properties  []OntologyProperty `json:"properties,omitempty"`
}

// OntologySchema is the full stored ontology.
type OntologySchema struct {
	SchemaID    string `json:"schema_id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Version     int    `json:"version"`
	Active      bool   `json:"active"`
	// StrictActions closes the generic upsert tools once action types are
	// defined, making actions the only write path. Defaults to false.
	StrictActions    bool                     `json:"strict_actions,omitempty"`
	Metadata         map[string]string        `json:"metadata,omitempty"`
	ObjectTypes      []OntologyObjectType     `json:"object_types,omitempty"`
	LinkTypes        []OntologyLinkType       `json:"link_types,omitempty"`
	InterfaceTypes   []OntologyInterfaceType  `json:"interface_types,omitempty"`
	SharedProperties []OntologyProperty       `json:"shared_properties,omitempty"`
	ActionTypes      []OntologyActionType     `json:"action_types,omitempty"`
	ObjectSets       []OntologyNamedObjectSet `json:"object_sets,omitempty"`
	CreatedAt        time.Time                `json:"created_at"`
	UpdatedAt        time.Time                `json:"updated_at"`
}
```

`OntologyActionType` 和 `OntologyNamedObjectSet` 在第 4/5 期才有内容。为了让第 1 期能编译，**在本任务里**同时创建两个占位文件：

`pkg/cortexdb/objectset_types.go`：

```go
package cortexdb

// OntologyNamedObjectSet is a saved, reusable object set definition.
// The ObjectSet algebra itself lands in phase 4.
type OntologyNamedObjectSet struct {
	APIName     string `json:"api_name"`
	Description string `json:"description,omitempty"`
}
```

`pkg/cortexdb/ontology_action_types.go`：

```go
package cortexdb

// OntologyActionType is the schema definition of a governed set of graph
// edits. The rule engine lands in phase 5.
type OntologyActionType struct {
	APIName     string `json:"api_name"`
	Description string `json:"description,omitempty"`
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./pkg/cortexdb -run 'TestOntologySchemaJSONRoundTrip|TestOntologyDataTypeNestedKinds' -v`
Expected: PASS ×2。

注意：此时 `ontology_validation.go`、`ontology_storage.go`、`ontology_api.go`、`ontology_test.go` 会因引用已删除的 `OntologyEntityType` 而编译失败，`go build ./...` 还过不了。这是预期的——Task 2–5 会依次修好。本步只要求这两个测试所在的包能编译到测试点，若整包无法编译，先把 Task 2–5 的骨架空实现补上再回来跑。为避免这种半破状态，**推荐把 Task 1–5 作为一个连续工作段完成后再 commit**。

- [ ] **Step 5: 暂不 commit**

Task 1–5 完成后统一 commit（见 Task 5 Step 5）。

---

### Task 2: API 名归一化与属性值解析

**Files:**
- Create: `pkg/cortexdb/ontology_normalize.go`
- Test: `pkg/cortexdb/ontology_normalize_test.go`

- [ ] **Step 1: 写失败的测试**

```go
package cortexdb

import "testing"

func TestValidateOntologyAPIName(t *testing.T) {
	valid := []string{"Airport", "flightDeparture", "a", "Object_Type_1"}
	for _, name := range valid {
		if err := validateOntologyAPIName("object type", name); err != nil {
			t.Fatalf("expected %q to be valid, got %v", name, err)
		}
	}

	invalid := []string{"", " ", "1Airport", "flight-departure", "flight departure", "flight.departure", "航班"}
	for _, name := range invalid {
		if err := validateOntologyAPIName("object type", name); err == nil {
			t.Fatalf("expected %q to be rejected", name)
		}
	}
}

func TestOntologyAPIKeyIsCaseInsensitive(t *testing.T) {
	if ontologyAPIKey("Airport") != ontologyAPIKey("airport") {
		t.Fatal("api key lookup must be case-insensitive")
	}
	if ontologyAPIKey("Airport") == ontologyAPIKey("Aircraft") {
		t.Fatal("distinct names must not collide")
	}
}

func TestParseOntologyPropertyValue(t *testing.T) {
	cases := []struct {
		kind    OntologyDataKind
		raw     string
		wantErr bool
	}{
		{OntologyDataString, "LHR", false},
		{OntologyDataInteger, "83", false},
		{OntologyDataInteger, "83.5", true},
		{OntologyDataInteger, "not-a-number", true},
		{OntologyDataDouble, "83.5", false},
		{OntologyDataBoolean, "true", false},
		{OntologyDataBoolean, "yes", true},
		{OntologyDataTimestamp, "2026-08-10T12:00:00Z", false},
		{OntologyDataTimestamp, "10/08/2026", true},
		{OntologyDataDate, "2026-08-10", false},
		{OntologyDataGeoPoint, "51.4700,-0.4543", false},
		{OntologyDataGeoPoint, "51.4700", true},
		{OntologyDataString, "", true},
	}
	for _, tc := range cases {
		err := parseOntologyPropertyValue(OntologyDataType{Kind: tc.kind}, tc.raw)
		if tc.wantErr && err == nil {
			t.Fatalf("kind=%s raw=%q: expected error", tc.kind, tc.raw)
		}
		if !tc.wantErr && err != nil {
			t.Fatalf("kind=%s raw=%q: unexpected error %v", tc.kind, tc.raw, err)
		}
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./pkg/cortexdb -run 'TestValidateOntologyAPIName|TestOntologyAPIKeyIsCaseInsensitive|TestParseOntologyPropertyValue' -v`
Expected: `undefined: validateOntologyAPIName`。

- [ ] **Step 3: 写实现**

```go
package cortexdb

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// ontologyAPINamePattern mirrors Foundry API names: an ASCII letter followed
// by letters, digits or underscores. Unlike the v1 normalizer this does not
// lowercase or rewrite the name, so Airport and airport stay distinguishable
// in display while still resolving to the same lookup key.
var ontologyAPINamePattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]*$`)

func validateOntologyAPIName(kind string, name string) error {
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("%s api name is required", kind)
	}
	if !ontologyAPINamePattern.MatchString(name) {
		return fmt.Errorf("%s api name %q must match [A-Za-z][A-Za-z0-9_]*", kind, name)
	}
	return nil
}

// ontologyAPIKey is the case-insensitive lookup key for an API name.
func ontologyAPIKey(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}

// parseOntologyPropertyValue checks that raw, as carried in a metadata map,
// is a legal value for the declared data type. Values arrive as strings
// because ToolEntityInput.Metadata is map[string]string.
func parseOntologyPropertyValue(dataType OntologyDataType, raw string) error {
	if strings.TrimSpace(raw) == "" {
		return fmt.Errorf("value is empty")
	}
	switch dataType.Kind {
	case OntologyDataString, OntologyDataMarking, OntologyDataGeoShape:
		return nil
	case OntologyDataInteger, OntologyDataLong:
		if _, err := strconv.ParseInt(raw, 10, 64); err != nil {
			return fmt.Errorf("value %q is not an %s", raw, dataType.Kind)
		}
	case OntologyDataDouble, OntologyDataDecimal:
		if _, err := strconv.ParseFloat(raw, 64); err != nil {
			return fmt.Errorf("value %q is not a %s", raw, dataType.Kind)
		}
	case OntologyDataBoolean:
		if raw != "true" && raw != "false" {
			return fmt.Errorf("value %q is not a boolean (want \"true\" or \"false\")", raw)
		}
	case OntologyDataDate:
		if _, err := time.Parse("2006-01-02", raw); err != nil {
			return fmt.Errorf("value %q is not a date (want YYYY-MM-DD)", raw)
		}
	case OntologyDataTimestamp:
		if _, err := time.Parse(time.RFC3339, raw); err != nil {
			return fmt.Errorf("value %q is not a timestamp (want RFC3339)", raw)
		}
	case OntologyDataGeoPoint:
		parts := strings.Split(raw, ",")
		if len(parts) != 2 {
			return fmt.Errorf("value %q is not a geopoint (want \"lat,lon\")", raw)
		}
		for _, part := range parts {
			if _, err := strconv.ParseFloat(strings.TrimSpace(part), 64); err != nil {
				return fmt.Errorf("value %q is not a geopoint (want \"lat,lon\")", raw)
			}
		}
	case OntologyDataVector, OntologyDataArray, OntologyDataStruct:
		// Composite values are carried out of band, not in the string
		// metadata map; presence is all that can be checked here.
		return nil
	default:
		return fmt.Errorf("unknown data type kind %q", dataType.Kind)
	}
	return nil
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./pkg/cortexdb -run 'TestValidateOntologyAPIName|TestOntologyAPIKeyIsCaseInsensitive|TestParseOntologyPropertyValue' -v`
Expected: PASS ×3。

- [ ] **Step 5: 暂不 commit**（随 Task 5 一起提交）

---

### Task 3: schema 自身合法性校验

**Files:**
- Create: `pkg/cortexdb/ontology_validation.go`（**先删除**旧文件全部内容）
- Test: `pkg/cortexdb/ontology_validation_test.go`

- [ ] **Step 1: 写失败的测试**

```go
package cortexdb

import (
	"strings"
	"testing"
)

func validAviationSchema() OntologySchema {
	return OntologySchema{
		SchemaID: "aviation",
		Name:     "Aviation",
		ObjectTypes: []OntologyObjectType{
			{
				APIName:    "Airport",
				PrimaryKey: "iataCode",
				Properties: []OntologyProperty{
					{APIName: "iataCode", DataType: OntologyDataType{Kind: OntologyDataString}, Required: true},
					{APIName: "airportName", DataType: OntologyDataType{Kind: OntologyDataString}},
				},
			},
			{
				APIName:    "Flight",
				PrimaryKey: "flightNumber",
				Properties: []OntologyProperty{
					{APIName: "flightNumber", DataType: OntologyDataType{Kind: OntologyDataString}, Required: true},
					{APIName: "originIata", DataType: OntologyDataType{Kind: OntologyDataString}},
				},
			},
		},
		LinkTypes: []OntologyLinkType{
			{
				APIName: "flightDeparture",
				A:       OntologyLinkSide{APIName: "departures", ObjectTypeAPIName: "Airport", Cardinality: OntologyCardinalityMany},
				B:       OntologyLinkSide{APIName: "origin", ObjectTypeAPIName: "Flight", Cardinality: OntologyCardinalityOne, ForeignKeyProperty: "originIata"},
			},
		},
	}
}

func TestValidateOntologySchemaAcceptsValid(t *testing.T) {
	if err := validateOntologySchema(validAviationSchema()); err != nil {
		t.Fatalf("expected valid schema, got %v", err)
	}
}

func TestValidateOntologySchemaRequiresPrimaryKey(t *testing.T) {
	schema := validAviationSchema()
	schema.ObjectTypes[0].PrimaryKey = ""

	err := validateOntologySchema(schema)
	if err == nil {
		t.Fatal("expected missing primary key to be rejected")
	}
	if !strings.Contains(err.Error(), "primary_key") {
		t.Fatalf("error should name primary_key, got %v", err)
	}
}

func TestValidateOntologySchemaPrimaryKeyMustBeDeclaredProperty(t *testing.T) {
	schema := validAviationSchema()
	schema.ObjectTypes[0].PrimaryKey = "notAProperty"

	err := validateOntologySchema(schema)
	if err == nil {
		t.Fatal("expected unknown primary key property to be rejected")
	}
	if !strings.Contains(err.Error(), "notAProperty") {
		t.Fatalf("error should name the property, got %v", err)
	}
}

func TestValidateOntologySchemaPrimaryKeyMustBeRequired(t *testing.T) {
	schema := validAviationSchema()
	schema.ObjectTypes[0].Properties[0].Required = false

	if err := validateOntologySchema(schema); err == nil {
		t.Fatal("expected optional primary key property to be rejected")
	}
}

func TestValidateOntologySchemaRejectsDuplicateObjectTypes(t *testing.T) {
	schema := validAviationSchema()
	schema.ObjectTypes = append(schema.ObjectTypes, OntologyObjectType{
		APIName:    "airport",
		PrimaryKey: "iataCode",
		Properties: []OntologyProperty{{APIName: "iataCode", DataType: OntologyDataType{Kind: OntologyDataString}, Required: true}},
	})

	if err := validateOntologySchema(schema); err == nil {
		t.Fatal("expected case-insensitive duplicate object type to be rejected")
	}
}

func TestValidateOntologySchemaLinkSideMustReferenceKnownObjectType(t *testing.T) {
	schema := validAviationSchema()
	schema.LinkTypes[0].B.ObjectTypeAPIName = "Spacecraft"

	err := validateOntologySchema(schema)
	if err == nil {
		t.Fatal("expected unknown link side object type to be rejected")
	}
	if !strings.Contains(err.Error(), "Spacecraft") {
		t.Fatalf("error should name the object type, got %v", err)
	}
}

func TestValidateOntologySchemaForeignKeyOnlyOnOneSide(t *testing.T) {
	schema := validAviationSchema()
	schema.LinkTypes[0].A.ForeignKeyProperty = "airportName"

	if err := validateOntologySchema(schema); err == nil {
		t.Fatal("expected foreign key on a MANY side to be rejected")
	}
}

func TestValidateOntologySchemaForeignKeyMustBeDeclaredProperty(t *testing.T) {
	schema := validAviationSchema()
	schema.LinkTypes[0].B.ForeignKeyProperty = "notAProperty"

	if err := validateOntologySchema(schema); err == nil {
		t.Fatal("expected unknown foreign key property to be rejected")
	}
}

func TestValidateOntologySchemaRejectsVectorWithoutDimension(t *testing.T) {
	schema := validAviationSchema()
	schema.ObjectTypes[0].Properties = append(schema.ObjectTypes[0].Properties, OntologyProperty{
		APIName:  "embedding",
		DataType: OntologyDataType{Kind: OntologyDataVector},
	})

	if err := validateOntologySchema(schema); err == nil {
		t.Fatal("expected vector property without dimension to be rejected")
	}
}

func TestValidateOntologySchemaTitlePropertyMustExist(t *testing.T) {
	schema := validAviationSchema()
	schema.ObjectTypes[0].TitleProperty = "nope"

	if err := validateOntologySchema(schema); err == nil {
		t.Fatal("expected unknown title property to be rejected")
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./pkg/cortexdb -run TestValidateOntologySchema -v`
Expected: `undefined: validateOntologySchema`。

- [ ] **Step 3: 写实现**

清空 `pkg/cortexdb/ontology_validation.go` 并写入：

```go
package cortexdb

import (
	"fmt"
	"strings"
)

// validateOntologySchema checks a schema for internal consistency before it
// is persisted. Everything it rejects is a modelling mistake that would
// otherwise surface much later as a confusing write-time failure.
func validateOntologySchema(schema OntologySchema) error {
	if strings.TrimSpace(schema.SchemaID) == "" {
		return fmt.Errorf("schema_id is required")
	}

	objectTypes := make(map[string]OntologyObjectType, len(schema.ObjectTypes))
	for _, objectType := range schema.ObjectTypes {
		if err := validateOntologyAPIName("object type", objectType.APIName); err != nil {
			return err
		}
		key := ontologyAPIKey(objectType.APIName)
		if _, exists := objectTypes[key]; exists {
			return fmt.Errorf("duplicate object type %q", objectType.APIName)
		}
		if err := validateOntologyObjectType(objectType); err != nil {
			return err
		}
		objectTypes[key] = objectType
	}

	interfaceTypes := make(map[string]OntologyInterfaceType, len(schema.InterfaceTypes))
	for _, interfaceType := range schema.InterfaceTypes {
		if err := validateOntologyAPIName("interface type", interfaceType.APIName); err != nil {
			return err
		}
		key := ontologyAPIKey(interfaceType.APIName)
		if _, exists := interfaceTypes[key]; exists {
			return fmt.Errorf("duplicate interface type %q", interfaceType.APIName)
		}
		if _, exists := objectTypes[key]; exists {
			return fmt.Errorf("interface type %q collides with an object type of the same name", interfaceType.APIName)
		}
		for _, property := range interfaceType.Properties {
			if err := validateOntologyProperty(interfaceType.APIName, property); err != nil {
				return err
			}
		}
		interfaceTypes[key] = interfaceType
	}

	for _, objectType := range schema.ObjectTypes {
		for _, implemented := range objectType.Implements {
			if _, exists := interfaceTypes[ontologyAPIKey(implemented)]; !exists {
				return fmt.Errorf("object type %q implements unknown interface %q", objectType.APIName, implemented)
			}
		}
	}

	linkTypes := make(map[string]struct{}, len(schema.LinkTypes))
	for _, linkType := range schema.LinkTypes {
		if err := validateOntologyAPIName("link type", linkType.APIName); err != nil {
			return err
		}
		key := ontologyAPIKey(linkType.APIName)
		if _, exists := linkTypes[key]; exists {
			return fmt.Errorf("duplicate link type %q", linkType.APIName)
		}
		linkTypes[key] = struct{}{}

		for _, side := range []OntologyLinkSide{linkType.A, linkType.B} {
			if err := validateOntologyLinkSide(linkType.APIName, side, objectTypes); err != nil {
				return err
			}
		}
		if ontologyAPIKey(linkType.A.APIName) == ontologyAPIKey(linkType.B.APIName) {
			return fmt.Errorf("link type %q needs distinct api names on each side", linkType.APIName)
		}
	}
	return nil
}

func validateOntologyObjectType(objectType OntologyObjectType) error {
	if strings.TrimSpace(objectType.PrimaryKey) == "" {
		return fmt.Errorf("object type %q must declare primary_key", objectType.APIName)
	}

	properties := make(map[string]OntologyProperty, len(objectType.Properties))
	for _, property := range objectType.Properties {
		if err := validateOntologyProperty(objectType.APIName, property); err != nil {
			return err
		}
		key := ontologyAPIKey(property.APIName)
		if _, exists := properties[key]; exists {
			return fmt.Errorf("object type %q has duplicate property %q", objectType.APIName, property.APIName)
		}
		properties[key] = property
	}

	primaryKey, ok := properties[ontologyAPIKey(objectType.PrimaryKey)]
	if !ok {
		return fmt.Errorf("object type %q declares primary_key %q which is not a declared property", objectType.APIName, objectType.PrimaryKey)
	}
	if !primaryKey.Required {
		return fmt.Errorf("object type %q primary key property %q must be required", objectType.APIName, objectType.PrimaryKey)
	}
	switch primaryKey.DataType.Kind {
	case OntologyDataString, OntologyDataInteger, OntologyDataLong:
	default:
		return fmt.Errorf("object type %q primary key property %q must be string, integer or long, got %q",
			objectType.APIName, objectType.PrimaryKey, primaryKey.DataType.Kind)
	}

	if strings.TrimSpace(objectType.TitleProperty) != "" {
		if _, ok := properties[ontologyAPIKey(objectType.TitleProperty)]; !ok {
			return fmt.Errorf("object type %q declares title_property %q which is not a declared property", objectType.APIName, objectType.TitleProperty)
		}
	}
	return nil
}

func validateOntologyProperty(owner string, property OntologyProperty) error {
	if err := validateOntologyAPIName(fmt.Sprintf("%s property", owner), property.APIName); err != nil {
		return err
	}
	return validateOntologyDataType(owner, property.APIName, property.DataType)
}

func validateOntologyDataType(owner string, propertyName string, dataType OntologyDataType) error {
	switch dataType.Kind {
	case OntologyDataString, OntologyDataInteger, OntologyDataLong, OntologyDataDouble,
		OntologyDataDecimal, OntologyDataBoolean, OntologyDataDate, OntologyDataTimestamp,
		OntologyDataGeoPoint, OntologyDataGeoShape, OntologyDataMarking:
		return nil
	case OntologyDataVector:
		if dataType.Dimension <= 0 {
			return fmt.Errorf("%s property %q is a vector and must declare a positive dimension", owner, propertyName)
		}
		return nil
	case OntologyDataArray:
		if dataType.ItemType == nil {
			return fmt.Errorf("%s property %q is an array and must declare item_type", owner, propertyName)
		}
		return validateOntologyDataType(owner, propertyName, *dataType.ItemType)
	case OntologyDataStruct:
		if len(dataType.Fields) == 0 {
			return fmt.Errorf("%s property %q is a struct and must declare fields", owner, propertyName)
		}
		for _, field := range dataType.Fields {
			if err := validateOntologyProperty(owner, field); err != nil {
				return err
			}
		}
		return nil
	default:
		return fmt.Errorf("%s property %q has unknown data type kind %q", owner, propertyName, dataType.Kind)
	}
}

func validateOntologyLinkSide(linkTypeName string, side OntologyLinkSide, objectTypes map[string]OntologyObjectType) error {
	if err := validateOntologyAPIName(fmt.Sprintf("link type %s side", linkTypeName), side.APIName); err != nil {
		return err
	}
	objectType, ok := objectTypes[ontologyAPIKey(side.ObjectTypeAPIName)]
	if !ok {
		return fmt.Errorf("link type %q side %q references unknown object type %q", linkTypeName, side.APIName, side.ObjectTypeAPIName)
	}
	switch side.Cardinality {
	case OntologyCardinalityOne, OntologyCardinalityMany:
	default:
		return fmt.Errorf("link type %q side %q must declare cardinality ONE or MANY, got %q", linkTypeName, side.APIName, side.Cardinality)
	}

	if strings.TrimSpace(side.ForeignKeyProperty) == "" {
		return nil
	}
	// Foundry backs a ONE side with a foreign key on the object; a MANY side
	// has nowhere to put one, so accepting it would silently do nothing.
	if side.Cardinality != OntologyCardinalityOne {
		return fmt.Errorf("link type %q side %q declares a foreign key but has cardinality MANY", linkTypeName, side.APIName)
	}
	for _, property := range objectType.Properties {
		if ontologyAPIKey(property.APIName) == ontologyAPIKey(side.ForeignKeyProperty) {
			return nil
		}
	}
	return fmt.Errorf("link type %q side %q declares foreign key %q which is not a property of %q",
		linkTypeName, side.APIName, side.ForeignKeyProperty, objectType.APIName)
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./pkg/cortexdb -run TestValidateOntologySchema -v`
Expected: PASS ×10。

- [ ] **Step 5: 暂不 commit**（随 Task 5 一起提交）

---

### Task 4: 编译后的查找表

**Files:**
- Create: `pkg/cortexdb/ontology_compile.go`
- Test: `pkg/cortexdb/ontology_compile_test.go`

- [ ] **Step 1: 写失败的测试**

```go
package cortexdb

import "testing"

func TestCompileOntologyLooksUpCaseInsensitively(t *testing.T) {
	compiled := compileOntology(validAviationSchema())

	objectType, ok := compiled.objectType("AIRPORT")
	if !ok {
		t.Fatal("expected case-insensitive object type lookup")
	}
	if objectType.APIName != "Airport" {
		t.Fatalf("lookup should return the declared casing, got %q", objectType.APIName)
	}

	if _, ok := compiled.linkType("FLIGHTDEPARTURE"); !ok {
		t.Fatal("expected case-insensitive link type lookup")
	}
	if _, ok := compiled.objectType("Spacecraft"); ok {
		t.Fatal("unknown object type must not resolve")
	}
}

func TestCompiledOntologyResolvesLinkSidesByEndpointType(t *testing.T) {
	compiled := compileOntology(validAviationSchema())
	linkType, _ := compiled.linkType("flightDeparture")

	from, to, err := compiled.orientLink(linkType, "Airport", "Flight")
	if err != nil {
		t.Fatalf("orient: %v", err)
	}
	if from.APIName != "departures" || to.APIName != "origin" {
		t.Fatalf("wrong orientation: from=%q to=%q", from.APIName, to.APIName)
	}

	from, to, err = compiled.orientLink(linkType, "Flight", "Airport")
	if err != nil {
		t.Fatalf("reverse orient: %v", err)
	}
	if from.APIName != "origin" || to.APIName != "departures" {
		t.Fatalf("wrong reverse orientation: from=%q to=%q", from.APIName, to.APIName)
	}

	if _, _, err := compiled.orientLink(linkType, "Airport", "Airport"); err == nil {
		t.Fatal("expected mismatched endpoint types to be rejected")
	}
}

func TestCompiledOntologyPropertyLookup(t *testing.T) {
	compiled := compileOntology(validAviationSchema())

	property, ok := compiled.property("Airport", "IATACODE")
	if !ok {
		t.Fatal("expected case-insensitive property lookup")
	}
	if property.APIName != "iataCode" {
		t.Fatalf("expected declared casing, got %q", property.APIName)
	}
	if _, ok := compiled.property("Airport", "nope"); ok {
		t.Fatal("unknown property must not resolve")
	}
}

func TestCompileOntologyOnEmptySchema(t *testing.T) {
	compiled := compileOntology(OntologySchema{})
	if compiled.isEmpty() != true {
		t.Fatal("empty schema should compile to an empty ontology")
	}
	if _, ok := compiled.objectType("Airport"); ok {
		t.Fatal("empty ontology must resolve nothing")
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./pkg/cortexdb -run 'TestCompileOntology|TestCompiledOntology' -v`
Expected: `undefined: compileOntology`。

- [ ] **Step 3: 写实现**

```go
package cortexdb

import "fmt"

// compiledOntology is the read-optimised form of a schema. Every validation
// and resolution path goes through it so name lookup rules stay in one place.
type compiledOntology struct {
	schema      OntologySchema
	objectTypes map[string]OntologyObjectType
	linkTypes   map[string]OntologyLinkType
	interfaces  map[string]OntologyInterfaceType
	properties  map[string]map[string]OntologyProperty
}

func compileOntology(schema OntologySchema) *compiledOntology {
	compiled := &compiledOntology{
		schema:      schema,
		objectTypes: make(map[string]OntologyObjectType, len(schema.ObjectTypes)),
		linkTypes:   make(map[string]OntologyLinkType, len(schema.LinkTypes)),
		interfaces:  make(map[string]OntologyInterfaceType, len(schema.InterfaceTypes)),
		properties:  make(map[string]map[string]OntologyProperty, len(schema.ObjectTypes)),
	}

	for _, objectType := range schema.ObjectTypes {
		key := ontologyAPIKey(objectType.APIName)
		compiled.objectTypes[key] = objectType

		byName := make(map[string]OntologyProperty, len(objectType.Properties))
		for _, property := range objectType.Properties {
			byName[ontologyAPIKey(property.APIName)] = property
		}
		compiled.properties[key] = byName
	}
	for _, linkType := range schema.LinkTypes {
		compiled.linkTypes[ontologyAPIKey(linkType.APIName)] = linkType
	}
	for _, interfaceType := range schema.InterfaceTypes {
		compiled.interfaces[ontologyAPIKey(interfaceType.APIName)] = interfaceType
	}
	return compiled
}

func (c *compiledOntology) isEmpty() bool {
	return len(c.objectTypes) == 0 && len(c.linkTypes) == 0 && len(c.interfaces) == 0
}

func (c *compiledOntology) objectType(apiName string) (OntologyObjectType, bool) {
	objectType, ok := c.objectTypes[ontologyAPIKey(apiName)]
	return objectType, ok
}

func (c *compiledOntology) linkType(apiName string) (OntologyLinkType, bool) {
	linkType, ok := c.linkTypes[ontologyAPIKey(apiName)]
	return linkType, ok
}

func (c *compiledOntology) interfaceType(apiName string) (OntologyInterfaceType, bool) {
	interfaceType, ok := c.interfaces[ontologyAPIKey(apiName)]
	return interfaceType, ok
}

func (c *compiledOntology) property(objectTypeAPIName string, propertyAPIName string) (OntologyProperty, bool) {
	byName, ok := c.properties[ontologyAPIKey(objectTypeAPIName)]
	if !ok {
		return OntologyProperty{}, false
	}
	property, ok := byName[ontologyAPIKey(propertyAPIName)]
	return property, ok
}

// orientLink decides which side of a link type a concrete edge runs from and
// to, given the object types of its endpoints. A link type is bidirectional,
// so the endpoint types are what fix the direction.
func (c *compiledOntology) orientLink(linkType OntologyLinkType, fromObjectType string, toObjectType string) (OntologyLinkSide, OntologyLinkSide, error) {
	fromKey := ontologyAPIKey(fromObjectType)
	toKey := ontologyAPIKey(toObjectType)
	aKey := ontologyAPIKey(linkType.A.ObjectTypeAPIName)
	bKey := ontologyAPIKey(linkType.B.ObjectTypeAPIName)

	switch {
	case fromKey == aKey && toKey == bKey:
		return linkType.A, linkType.B, nil
	case fromKey == bKey && toKey == aKey:
		return linkType.B, linkType.A, nil
	default:
		return OntologyLinkSide{}, OntologyLinkSide{}, fmt.Errorf(
			"link type %q connects %s and %s, not %s and %s",
			linkType.APIName, linkType.A.ObjectTypeAPIName, linkType.B.ObjectTypeAPIName,
			fromObjectType, toObjectType)
	}
}
```

注意：自引用链接（两侧同一对象类型）在 `orientLink` 里会命中第一个 case，方向按 A→B 解释，这是正确行为。

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./pkg/cortexdb -run 'TestCompileOntology|TestCompiledOntology' -v`
Expected: PASS ×4。

- [ ] **Step 5: 暂不 commit**（随 Task 5 一起提交）

---

### Task 5: 新表存储 + API + 工具面

**Files:**
- Create: `pkg/cortexdb/ontology_storage.go`（**先删除**旧文件全部内容）
- Modify: `pkg/cortexdb/ontology_api.go`（重写请求/响应类型）
- Modify: `pkg/cortexdb/ontology_tooldefs.go:3-50`（入参 schema 换 v2）
- Delete: `pkg/cortexdb/ontology_test.go` 里 `TestOntologySchemaAPIAndToolValidation`（引用已删类型，由 Task 7 的新测试取代）
- Test: `pkg/cortexdb/ontology_storage_test.go`

- [ ] **Step 1: 写失败的测试**

```go
package cortexdb

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"
)

func openOntologyTestDB(t *testing.T) *DB {
	t.Helper()
	dbPath := fmt.Sprintf("test_ontology_v2_%d.db", time.Now().UnixNano())
	db, err := Open(DefaultConfig(dbPath))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
		for _, suffix := range []string{"", "-wal", "-shm"} {
			_ = os.Remove(dbPath + suffix)
		}
	})
	return db
}

func TestSaveAndGetOntologySchema(t *testing.T) {
	db := openOntologyTestDB(t)
	ctx := context.Background()

	schema := validAviationSchema()
	saved, err := db.SaveOntologySchema(ctx, OntologySaveRequest{Schema: schema, Activate: true})
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	if saved.Schema.Version != 1 {
		t.Fatalf("expected version 1, got %d", saved.Schema.Version)
	}
	if !saved.Schema.Active {
		t.Fatal("expected schema to be active")
	}

	got, err := db.GetOntologySchema(ctx, OntologyGetRequest{SchemaID: "aviation"})
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(got.Schema.ObjectTypes) != 2 {
		t.Fatalf("expected 2 object types, got %d", len(got.Schema.ObjectTypes))
	}
	if got.Schema.ObjectTypes[0].APIName != "Airport" {
		t.Fatalf("casing not preserved through storage: %q", got.Schema.ObjectTypes[0].APIName)
	}
	if got.Schema.LinkTypes[0].B.Cardinality != OntologyCardinalityOne {
		t.Fatalf("cardinality not preserved: %q", got.Schema.LinkTypes[0].B.Cardinality)
	}
}

func TestSaveOntologySchemaRejectsInvalidSchema(t *testing.T) {
	db := openOntologyTestDB(t)
	ctx := context.Background()

	schema := validAviationSchema()
	schema.ObjectTypes[0].PrimaryKey = ""

	if _, err := db.SaveOntologySchema(ctx, OntologySaveRequest{Schema: schema}); err == nil {
		t.Fatal("expected invalid schema to be rejected at save time")
	}
}

func TestSaveOntologySchemaAutoIncrementsVersion(t *testing.T) {
	db := openOntologyTestDB(t)
	ctx := context.Background()

	if _, err := db.SaveOntologySchema(ctx, OntologySaveRequest{Schema: validAviationSchema()}); err != nil {
		t.Fatalf("first save: %v", err)
	}
	second, err := db.SaveOntologySchema(ctx, OntologySaveRequest{Schema: validAviationSchema()})
	if err != nil {
		t.Fatalf("second save: %v", err)
	}
	if second.Schema.Version != 2 {
		t.Fatalf("expected version 2, got %d", second.Schema.Version)
	}
}

func TestActivatingOneSchemaDeactivatesOthers(t *testing.T) {
	db := openOntologyTestDB(t)
	ctx := context.Background()

	first := validAviationSchema()
	if _, err := db.SaveOntologySchema(ctx, OntologySaveRequest{Schema: first, Activate: true}); err != nil {
		t.Fatalf("save first: %v", err)
	}

	second := validAviationSchema()
	second.SchemaID = "aviation-2"
	if _, err := db.SaveOntologySchema(ctx, OntologySaveRequest{Schema: second, Activate: true}); err != nil {
		t.Fatalf("save second: %v", err)
	}

	listed, err := db.ListOntologySchemas(ctx, OntologyListRequest{ActiveOnly: true})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(listed.Schemas) != 1 {
		t.Fatalf("expected exactly 1 active schema, got %d", len(listed.Schemas))
	}
	if listed.Schemas[0].SchemaID != "aviation-2" {
		t.Fatalf("expected aviation-2 active, got %q", listed.Schemas[0].SchemaID)
	}
}

func TestDeleteOntologySchema(t *testing.T) {
	db := openOntologyTestDB(t)
	ctx := context.Background()

	if _, err := db.SaveOntologySchema(ctx, OntologySaveRequest{Schema: validAviationSchema()}); err != nil {
		t.Fatalf("save: %v", err)
	}
	deleted, err := db.DeleteOntologySchema(ctx, OntologyDeleteRequest{SchemaID: "aviation"})
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if !deleted.Deleted {
		t.Fatal("expected Deleted=true")
	}
	if _, err := db.GetOntologySchema(ctx, OntologyGetRequest{SchemaID: "aviation"}); err == nil {
		t.Fatal("expected get after delete to fail")
	}
}

func TestOntologyV2UsesItsOwnTable(t *testing.T) {
	db := openOntologyTestDB(t)
	ctx := context.Background()

	if _, err := db.SaveOntologySchema(ctx, OntologySaveRequest{Schema: validAviationSchema()}); err != nil {
		t.Fatalf("save: %v", err)
	}

	var count int
	row := db.store.GetDB().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='ontology_schemas_v2'`)
	if err := row.Scan(&count); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if count != 1 {
		t.Fatal("expected v2 schemas to live in ontology_schemas_v2")
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./pkg/cortexdb -run 'TestSaveAndGetOntologySchema|TestSaveOntologySchema|TestActivatingOneSchema|TestDeleteOntologySchema|TestOntologyV2UsesItsOwnTable' -v`
Expected: 编译失败，`OntologySaveRequest` 没有 `Schema` 字段。

- [ ] **Step 3: 写实现**

先重写 `pkg/cortexdb/ontology_api.go` 的请求/响应类型（保留文件底部四个 `GraphRAGToolbox` 转发方法不变）：

```go
package cortexdb

import "context"

// OntologySaveRequest stores or updates a v2 ontology schema.
type OntologySaveRequest struct {
	Schema     OntologySchema `json:"schema"`
	Activate   bool           `json:"activate,omitempty"`
	Deactivate bool           `json:"deactivate,omitempty"`
}

// OntologySaveResponse returns the persisted schema.
type OntologySaveResponse struct {
	Schema OntologySchema `json:"schema"`
}

// OntologyGetRequest fetches one ontology schema by ID.
type OntologyGetRequest struct {
	SchemaID string `json:"schema_id"`
}

// OntologyGetResponse returns one ontology schema.
type OntologyGetResponse struct {
	Schema OntologySchema `json:"schema"`
}

// OntologyListRequest lists ontology schemas.
type OntologyListRequest struct {
	ActiveOnly bool `json:"active_only,omitempty"`
}

// OntologyListResponse returns ontology schemas.
type OntologyListResponse struct {
	Schemas []OntologySchema `json:"schemas"`
}

// OntologyDeleteRequest deletes one ontology schema by ID.
type OntologyDeleteRequest struct {
	SchemaID string `json:"schema_id"`
}

// OntologyDeleteResponse confirms ontology deletion.
type OntologyDeleteResponse struct {
	SchemaID string `json:"schema_id"`
	Deleted  bool   `json:"deleted"`
}

func (db *DB) SaveOntologySchema(ctx context.Context, req OntologySaveRequest) (*OntologySaveResponse, error) {
	schema, err := db.saveOntologySchemaRecord(ctx, req)
	if err != nil {
		return nil, err
	}
	return &OntologySaveResponse{Schema: *schema}, nil
}

func (db *DB) GetOntologySchema(ctx context.Context, req OntologyGetRequest) (*OntologyGetResponse, error) {
	schema, err := db.loadOntologySchema(ctx, req.SchemaID)
	if err != nil {
		return nil, err
	}
	return &OntologyGetResponse{Schema: *schema}, nil
}

func (db *DB) ListOntologySchemas(ctx context.Context, req OntologyListRequest) (*OntologyListResponse, error) {
	schemas, err := db.listOntologySchemaRecords(ctx, req.ActiveOnly)
	if err != nil {
		return nil, err
	}
	return &OntologyListResponse{Schemas: schemas}, nil
}

func (db *DB) DeleteOntologySchema(ctx context.Context, req OntologyDeleteRequest) (*OntologyDeleteResponse, error) {
	deleted, err := db.deleteOntologySchemaRecord(ctx, req.SchemaID)
	if err != nil {
		return nil, err
	}
	return &OntologyDeleteResponse{SchemaID: req.SchemaID, Deleted: deleted}, nil
}

func (t *GraphRAGToolbox) SaveOntologySchema(ctx context.Context, req OntologySaveRequest) (*OntologySaveResponse, error) {
	return t.db.SaveOntologySchema(ctx, req)
}

func (t *GraphRAGToolbox) GetOntologySchema(ctx context.Context, req OntologyGetRequest) (*OntologyGetResponse, error) {
	return t.db.GetOntologySchema(ctx, req)
}

func (t *GraphRAGToolbox) ListOntologySchemas(ctx context.Context, req OntologyListRequest) (*OntologyListResponse, error) {
	return t.db.ListOntologySchemas(ctx, req)
}

func (t *GraphRAGToolbox) DeleteOntologySchema(ctx context.Context, req OntologyDeleteRequest) (*OntologyDeleteResponse, error) {
	return t.db.DeleteOntologySchema(ctx, req)
}
```

再清空 `pkg/cortexdb/ontology_storage.go` 并写入：

```go
package cortexdb

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/liliang-cn/cortexdb/v2/pkg/core"
)

// ensureOntologySchemaTable creates the v2 table. The v1 table
// graph_ontology_schemas is deliberately left alone: v2 neither reads nor
// writes nor drops it, so an upgrade cannot corrupt or lose old rows.
func (db *DB) ensureOntologySchemaTable(ctx context.Context) error {
	db.ontologySchemaInit.Do(func() {
		schema := `
		CREATE TABLE IF NOT EXISTS ontology_schemas_v2 (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			version INTEGER NOT NULL DEFAULT 1,
			description TEXT,
			strict_actions INTEGER NOT NULL DEFAULT 0,
			metadata TEXT,
			definition TEXT NOT NULL,
			is_active INTEGER NOT NULL DEFAULT 0,
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		);
		CREATE INDEX IF NOT EXISTS idx_ontology_schemas_v2_active ON ontology_schemas_v2(is_active);
		`
		_, db.ontologySchemaInitErr = db.store.GetDB().ExecContext(ctx, schema)
	})
	return db.ontologySchemaInitErr
}

// ontologySchemaDefinition is the JSON blob stored in the definition column.
// The scalar columns above are duplicated here only where they are cheap;
// everything structural lives in this blob so adding type kinds needs no
// migration.
type ontologySchemaDefinition struct {
	ObjectTypes      []OntologyObjectType     `json:"object_types,omitempty"`
	LinkTypes        []OntologyLinkType       `json:"link_types,omitempty"`
	InterfaceTypes   []OntologyInterfaceType  `json:"interface_types,omitempty"`
	SharedProperties []OntologyProperty       `json:"shared_properties,omitempty"`
	ActionTypes      []OntologyActionType     `json:"action_types,omitempty"`
	ObjectSets       []OntologyNamedObjectSet `json:"object_sets,omitempty"`
}

func (db *DB) saveOntologySchemaRecord(ctx context.Context, req OntologySaveRequest) (*OntologySchema, error) {
	schema := req.Schema
	if req.Activate && req.Deactivate {
		return nil, fmt.Errorf("activate and deactivate cannot both be true")
	}
	if err := validateOntologySchema(schema); err != nil {
		return nil, err
	}
	if err := db.ensureOntologySchemaTable(ctx); err != nil {
		return nil, fmt.Errorf("init ontology schema table: %w", err)
	}

	definitionJSON, err := json.Marshal(ontologySchemaDefinition{
		ObjectTypes:      schema.ObjectTypes,
		LinkTypes:        schema.LinkTypes,
		InterfaceTypes:   schema.InterfaceTypes,
		SharedProperties: schema.SharedProperties,
		ActionTypes:      schema.ActionTypes,
		ObjectSets:       schema.ObjectSets,
	})
	if err != nil {
		return nil, fmt.Errorf("encode ontology definition: %w", err)
	}
	metadataJSON, err := json.Marshal(cloneStringMap(schema.Metadata))
	if err != nil {
		return nil, fmt.Errorf("encode ontology metadata: %w", err)
	}

	tx, err := db.store.GetDB().BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin ontology transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	existing, err := db.getOntologySchemaTx(ctx, tx, schema.SchemaID)
	if err != nil && !errors.Is(err, core.ErrNotFound) {
		return nil, err
	}

	version := schema.Version
	if version <= 0 {
		version = 1
		if existing != nil {
			version = existing.Version + 1
		}
	}

	active := false
	if existing != nil {
		active = existing.Active
	}
	if req.Activate {
		active = true
	}
	if req.Deactivate {
		active = false
	}
	if active {
		if _, err := tx.ExecContext(ctx,
			`UPDATE ontology_schemas_v2 SET is_active = 0, updated_at = CURRENT_TIMESTAMP WHERE is_active = 1 AND id <> ?`,
			schema.SchemaID); err != nil {
			return nil, fmt.Errorf("deactivate ontology schemas: %w", err)
		}
	}

	name := strings.TrimSpace(schema.Name)
	if name == "" {
		name = schema.SchemaID
	}

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO ontology_schemas_v2 (id, name, version, description, strict_actions, metadata, definition, is_active, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(id) DO UPDATE SET
			name = excluded.name,
			version = excluded.version,
			description = excluded.description,
			strict_actions = excluded.strict_actions,
			metadata = excluded.metadata,
			definition = excluded.definition,
			is_active = excluded.is_active,
			updated_at = CURRENT_TIMESTAMP
	`, schema.SchemaID, name, version, strings.TrimSpace(schema.Description),
		boolToInt(schema.StrictActions), string(metadataJSON), string(definitionJSON),
		boolToInt(active)); err != nil {
		return nil, fmt.Errorf("upsert ontology schema: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit ontology schema: %w", err)
	}
	return db.loadOntologySchema(ctx, schema.SchemaID)
}

const ontologySchemaColumns = `id, name, version, description, strict_actions, metadata, definition, is_active, created_at, updated_at`

func (db *DB) loadOntologySchema(ctx context.Context, schemaID string) (*OntologySchema, error) {
	if strings.TrimSpace(schemaID) == "" {
		return nil, fmt.Errorf("schema_id is required")
	}
	if err := db.ensureOntologySchemaTable(ctx); err != nil {
		return nil, fmt.Errorf("init ontology schema table: %w", err)
	}
	return db.getOntologySchemaTx(ctx, db.store.GetDB(), schemaID)
}

func (db *DB) listOntologySchemaRecords(ctx context.Context, activeOnly bool) ([]OntologySchema, error) {
	if err := db.ensureOntologySchemaTable(ctx); err != nil {
		return nil, fmt.Errorf("init ontology schema table: %w", err)
	}

	query := `SELECT ` + ontologySchemaColumns + ` FROM ontology_schemas_v2`
	if activeOnly {
		query += ` WHERE is_active = 1`
	}
	query += ` ORDER BY id`

	rows, err := db.store.GetDB().QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("list ontology schemas: %w", err)
	}
	defer func() { _ = rows.Close() }()

	schemas := make([]OntologySchema, 0)
	for rows.Next() {
		schema, err := scanOntologySchema(rows)
		if err != nil {
			return nil, err
		}
		schemas = append(schemas, *schema)
	}
	return schemas, rows.Err()
}

func (db *DB) deleteOntologySchemaRecord(ctx context.Context, schemaID string) (bool, error) {
	if strings.TrimSpace(schemaID) == "" {
		return false, fmt.Errorf("schema_id is required")
	}
	if err := db.ensureOntologySchemaTable(ctx); err != nil {
		return false, fmt.Errorf("init ontology schema table: %w", err)
	}
	result, err := db.store.GetDB().ExecContext(ctx, `DELETE FROM ontology_schemas_v2 WHERE id = ?`, schemaID)
	if err != nil {
		return false, fmt.Errorf("delete ontology schema: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("delete ontology schema rows: %w", err)
	}
	return affected > 0, nil
}

func (db *DB) loadActiveOntologySchema(ctx context.Context) (*OntologySchema, error) {
	schemas, err := db.listOntologySchemaRecords(ctx, true)
	if err != nil {
		return nil, err
	}
	if len(schemas) == 0 {
		return nil, nil
	}
	return &schemas[0], nil
}

type ontologyRowQuerier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

type ontologyRowScanner interface {
	Scan(dest ...any) error
}

func (db *DB) getOntologySchemaTx(ctx context.Context, querier ontologyRowQuerier, schemaID string) (*OntologySchema, error) {
	row := querier.QueryRowContext(ctx,
		`SELECT `+ontologySchemaColumns+` FROM ontology_schemas_v2 WHERE id = ?`, schemaID)
	schema, err := scanOntologySchema(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, core.ErrNotFound
	}
	return schema, err
}

func scanOntologySchema(scanner ontologyRowScanner) (*OntologySchema, error) {
	var (
		schema        OntologySchema
		description   sql.NullString
		metadataJSON  sql.NullString
		definitionRaw string
		strictActions int
		isActive      int
		createdAt     time.Time
		updatedAt     time.Time
	)
	if err := scanner.Scan(&schema.SchemaID, &schema.Name, &schema.Version, &description,
		&strictActions, &metadataJSON, &definitionRaw, &isActive, &createdAt, &updatedAt); err != nil {
		return nil, err
	}

	schema.Description = description.String
	schema.StrictActions = strictActions == 1
	schema.Active = isActive == 1
	schema.CreatedAt = createdAt
	schema.UpdatedAt = updatedAt

	if metadataJSON.Valid && metadataJSON.String != "" {
		if err := json.Unmarshal([]byte(metadataJSON.String), &schema.Metadata); err != nil {
			return nil, fmt.Errorf("decode ontology metadata: %w", err)
		}
	}

	var definition ontologySchemaDefinition
	if err := json.Unmarshal([]byte(definitionRaw), &definition); err != nil {
		return nil, fmt.Errorf("decode ontology definition: %w", err)
	}
	schema.ObjectTypes = definition.ObjectTypes
	schema.LinkTypes = definition.LinkTypes
	schema.InterfaceTypes = definition.InterfaceTypes
	schema.SharedProperties = definition.SharedProperties
	schema.ActionTypes = definition.ActionTypes
	schema.ObjectSets = definition.ObjectSets
	return &schema, nil
}

func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
```

最后把 `pkg/cortexdb/ontology_tooldefs.go` 里 `ontology_save` 的 `InputSchema` 换成 v2 形状（`ontology_get`/`ontology_list`/`ontology_delete` 三个不变）：

```go
{
	Name:        "ontology_save",
	Description: "Store or update an ontology schema: object types with typed properties and a mandatory primary key, and link types with per-side cardinality. Mark one schema active to make it the write-time validator.",
	InputSchema: toolObjectSchema(
		[]string{"schema"},
		map[string]any{
			"schema": map[string]any{
				"type":        "object",
				"description": "Full ontology schema. Required keys: schema_id. Optional: name, description, version, strict_actions, metadata, object_types, link_types, interface_types, shared_properties. Each object_type needs api_name, primary_key and properties[]; each property needs api_name and data_type.kind (string|integer|long|double|decimal|boolean|date|timestamp|geopoint|geoshape|vector|array|struct|marking). Each link_type needs api_name plus sides a and b, each with api_name, object_type_api_name and cardinality (ONE|MANY).",
			},
			"activate":   toolBooleanSchema("Set true to make this the active ontology schema."),
			"deactivate": toolBooleanSchema("Set true to deactivate this schema without deleting it."),
		},
	),
},
```

同时删除 `ontology_tooldefs.go` 底部的 `toolOntologyEntityTypesSchema` 和 `toolOntologyRelationTypesSchema` 两个辅助函数（已无引用），以及 `pkg/cortexdb/ontology_test.go` 中的 `TestOntologySchemaAPIAndToolValidation`（保留文件里的 `typedFixtureExtractor`，Task 7 会用到）。

- [ ] **Step 4: 跑测试确认通过**

Run: `go build ./... && go test ./pkg/cortexdb -run 'TestSaveAndGetOntologySchema|TestSaveOntologySchema|TestActivatingOneSchema|TestDeleteOntologySchema|TestOntologyV2UsesItsOwnTable' -v`
Expected: build 成功；PASS ×6。

若 `go build ./...` 报 `ontology_validation.go` 里旧的 `validateEntityInputs` / `validateRelationInputs` / `validateExtractedGraphData` 已被删除但调用点还在，**临时**在 `ontology_validation.go` 末尾加三个直通桩，Task 7 会用真实实现替换：

```go
func (db *DB) validateEntityInputs(_ context.Context, _ []ToolEntityInput) error { return nil }

func (db *DB) validateRelationInputs(_ context.Context, _ []ToolRelationInput) error { return nil }

func (db *DB) validateExtractedGraphData(_ context.Context, _ map[string]GraphEntity, _ map[string]graph.GraphEdge) error {
	return nil
}
```

（需要在文件顶部 import `"context"` 和 `"github.com/liliang-cn/cortexdb/v2/pkg/graph"`。）

- [ ] **Step 5: Commit 第 1 期**

```bash
go test -race ./pkg/cortexdb
git add pkg/cortexdb/ontology_types.go pkg/cortexdb/ontology_normalize.go \
        pkg/cortexdb/ontology_compile.go pkg/cortexdb/ontology_validation.go \
        pkg/cortexdb/ontology_storage.go pkg/cortexdb/ontology_api.go \
        pkg/cortexdb/ontology_tooldefs.go pkg/cortexdb/ontology_test.go \
        pkg/cortexdb/objectset_types.go pkg/cortexdb/ontology_action_types.go \
        pkg/cortexdb/ontology_types_test.go pkg/cortexdb/ontology_normalize_test.go \
        pkg/cortexdb/ontology_compile_test.go pkg/cortexdb/ontology_validation_test.go \
        pkg/cortexdb/ontology_storage_test.go
git commit -m "feat(ontology): replace ontology-lite with a typed v2 schema

Object types now carry typed properties and a mandatory primary key, and
link types carry per-side cardinality the way Foundry models them. v2
schemas live in their own table; the v1 table is left untouched."
```

---

# 第 2 期：接入写入路径

第 1 期的 schema 只是存起来了，Task 5 还留了三个直通桩。本期让 schema 真正开始约束图写入。

### Task 6: 主键 → 节点 ID

**Files:**
- Create: `pkg/cortexdb/ontology_identity.go`
- Test: `pkg/cortexdb/ontology_identity_test.go`

现状：`graphrag_tool_helpers.go:130` 的 `resolveEntityNodeID` 靠 name 推 ID（`entity:<normalized-name>`），所以同名不同物的实体会合并。有了强制主键后，节点 ID 改由 `对象类型 + 主键值` 决定。

- [ ] **Step 1: 写失败的测试**

```go
package cortexdb

import "testing"

func TestOntologyNodeIDUsesObjectTypeAndPrimaryKey(t *testing.T) {
	id := ontologyNodeID("Airport", "LHR")
	if id != "entity:airport:lhr" {
		t.Fatalf("unexpected node id: %q", id)
	}
	if ontologyNodeID("Airport", "LHR") != ontologyNodeID("airport", "lhr") {
		t.Fatal("node id must be case-insensitive")
	}
	if ontologyNodeID("Airport", "LHR") == ontologyNodeID("Aircraft", "LHR") {
		t.Fatal("same key under different object types must not collide")
	}
}

func TestOntologyNodeIDNormalizesSeparators(t *testing.T) {
	if ontologyNodeID("Airport", "London Heathrow") != "entity:airport:london_heathrow" {
		t.Fatalf("unexpected node id: %q", ontologyNodeID("Airport", "London Heathrow"))
	}
}

func TestResolveOntologyPrimaryKeyValue(t *testing.T) {
	compiled := compileOntology(validAviationSchema())

	value, err := resolveOntologyPrimaryKeyValue(compiled, "Airport", ToolEntityInput{
		Name:     "London Heathrow",
		Type:     "Airport",
		Metadata: map[string]string{"iataCode": "LHR"},
	})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if value != "LHR" {
		t.Fatalf("expected LHR, got %q", value)
	}
}

func TestResolveOntologyPrimaryKeyValueIsCaseInsensitiveOnMetadataKey(t *testing.T) {
	compiled := compileOntology(validAviationSchema())

	value, err := resolveOntologyPrimaryKeyValue(compiled, "Airport", ToolEntityInput{
		Type:     "Airport",
		Metadata: map[string]string{"IATACODE": "LHR"},
	})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if value != "LHR" {
		t.Fatalf("expected LHR, got %q", value)
	}
}

func TestResolveOntologyPrimaryKeyValueFallsBackToNameProperty(t *testing.T) {
	schema := validAviationSchema()
	schema.ObjectTypes[0].PrimaryKey = "airportName"
	schema.ObjectTypes[0].Properties[1].Required = true
	compiled := compileOntology(schema)

	value, err := resolveOntologyPrimaryKeyValue(compiled, "Airport", ToolEntityInput{Name: "London Heathrow"})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if value != "London Heathrow" {
		t.Fatalf("expected the entity name, got %q", value)
	}
}

func TestResolveOntologyPrimaryKeyValueErrorsWhenMissing(t *testing.T) {
	compiled := compileOntology(validAviationSchema())

	_, err := resolveOntologyPrimaryKeyValue(compiled, "Airport", ToolEntityInput{Name: "London Heathrow"})
	if err == nil {
		t.Fatal("expected a missing primary key value to be an error")
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./pkg/cortexdb -run 'TestOntologyNodeID|TestResolveOntologyPrimaryKeyValue' -v`
Expected: `undefined: ontologyNodeID`。

- [ ] **Step 3: 写实现**

```go
package cortexdb

import (
	"fmt"
	"strings"
	"unicode"
)

// ontologyNodeID derives a graph node ID from an object type and a primary
// key value. Prefixing with the object type is what stops two unrelated
// objects that happen to share a key from colliding, which is exactly what
// the old name-derived IDs could not prevent.
func ontologyNodeID(objectTypeAPIName string, primaryKeyValue string) string {
	return fmt.Sprintf("entity:%s:%s",
		normalizeOntologyIDPart(objectTypeAPIName),
		normalizeOntologyIDPart(primaryKeyValue))
}

func normalizeOntologyIDPart(value string) string {
	lowered := strings.ToLower(strings.TrimSpace(value))
	var b strings.Builder
	for _, r := range lowered {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(r)
		case r == ' ' || r == '-' || r == '_':
			b.WriteRune('_')
		}
	}
	return b.String()
}

// resolveOntologyPrimaryKeyValue pulls an entity's primary key value out of
// its metadata. When the primary key property doubles as the object's title
// the caller may supply it as the entity name instead, which is the common
// shape coming out of extraction.
func resolveOntologyPrimaryKeyValue(compiled *compiledOntology, objectTypeAPIName string, entity ToolEntityInput) (string, error) {
	objectType, ok := compiled.objectType(objectTypeAPIName)
	if !ok {
		return "", fmt.Errorf("unknown object type %q", objectTypeAPIName)
	}

	primaryKeyKey := ontologyAPIKey(objectType.PrimaryKey)
	for key, value := range entity.Metadata {
		if ontologyAPIKey(key) == primaryKeyKey && strings.TrimSpace(value) != "" {
			return value, nil
		}
	}

	// Fall back to the entity name only when the primary key is also the
	// title property, or when no title property is declared and the primary
	// key is the object's human-readable name.
	titleKey := ontologyAPIKey(objectType.TitleProperty)
	if (titleKey == primaryKeyKey || objectType.TitleProperty == "") && strings.TrimSpace(entity.Name) != "" {
		return entity.Name, nil
	}

	return "", fmt.Errorf("entity of type %q is missing primary key property %q (supply it in metadata)",
		objectType.APIName, objectType.PrimaryKey)
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./pkg/cortexdb -run 'TestOntologyNodeID|TestResolveOntologyPrimaryKeyValue' -v`
Expected: PASS ×6。

- [ ] **Step 5: Commit**

```bash
git add pkg/cortexdb/ontology_identity.go pkg/cortexdb/ontology_identity_test.go
git commit -m "feat(ontology): derive node IDs from object type and primary key"
```

---

### Task 7: 实体写入准入校验

**Files:**
- Modify: `pkg/cortexdb/ontology_validation.go`（替换 Task 5 留下的 `validateEntityInputs` 桩）
- Test: `pkg/cortexdb/ontology_write_validation_test.go`

- [ ] **Step 1: 写失败的测试**

```go
package cortexdb

import (
	"context"
	"strings"
	"testing"
)

func activateAviationSchema(t *testing.T, db *DB) {
	t.Helper()
	if _, err := db.SaveOntologySchema(context.Background(), OntologySaveRequest{
		Schema:   validAviationSchema(),
		Activate: true,
	}); err != nil {
		t.Fatalf("activate schema: %v", err)
	}
}

func TestValidateEntityInputsAcceptsConformingEntity(t *testing.T) {
	db := openOntologyTestDB(t)
	activateAviationSchema(t, db)

	err := db.validateEntityInputs(context.Background(), []ToolEntityInput{
		{Name: "London Heathrow", Type: "Airport", Metadata: map[string]string{"iataCode": "LHR"}},
	})
	if err != nil {
		t.Fatalf("expected conforming entity to pass, got %v", err)
	}
}

func TestValidateEntityInputsRejectsUnknownObjectType(t *testing.T) {
	db := openOntologyTestDB(t)
	activateAviationSchema(t, db)

	err := db.validateEntityInputs(context.Background(), []ToolEntityInput{
		{Name: "Voyager", Type: "Spacecraft", Metadata: map[string]string{"iataCode": "VGR"}},
	})
	if err == nil || !strings.Contains(err.Error(), "Spacecraft") {
		t.Fatalf("expected unknown object type to be rejected, got %v", err)
	}
}

func TestValidateEntityInputsRejectsMissingPrimaryKey(t *testing.T) {
	db := openOntologyTestDB(t)
	activateAviationSchema(t, db)

	err := db.validateEntityInputs(context.Background(), []ToolEntityInput{
		{Name: "London Heathrow", Type: "Airport"},
	})
	if err == nil || !strings.Contains(err.Error(), "iataCode") {
		t.Fatalf("expected missing primary key to be rejected, got %v", err)
	}
}

func TestValidateEntityInputsRejectsMissingRequiredProperty(t *testing.T) {
	db := openOntologyTestDB(t)

	schema := validAviationSchema()
	schema.ObjectTypes[0].Properties[1].Required = true // airportName
	if _, err := db.SaveOntologySchema(context.Background(), OntologySaveRequest{Schema: schema, Activate: true}); err != nil {
		t.Fatalf("activate: %v", err)
	}

	err := db.validateEntityInputs(context.Background(), []ToolEntityInput{
		{Name: "London Heathrow", Type: "Airport", Metadata: map[string]string{"iataCode": "LHR"}},
	})
	if err == nil || !strings.Contains(err.Error(), "airportName") {
		t.Fatalf("expected missing required property to be rejected, got %v", err)
	}
}

func TestValidateEntityInputsRejectsWrongDataType(t *testing.T) {
	db := openOntologyTestDB(t)

	schema := validAviationSchema()
	schema.ObjectTypes[0].Properties = append(schema.ObjectTypes[0].Properties, OntologyProperty{
		APIName:  "elevation",
		DataType: OntologyDataType{Kind: OntologyDataInteger},
	})
	if _, err := db.SaveOntologySchema(context.Background(), OntologySaveRequest{Schema: schema, Activate: true}); err != nil {
		t.Fatalf("activate: %v", err)
	}

	err := db.validateEntityInputs(context.Background(), []ToolEntityInput{
		{Name: "London Heathrow", Type: "Airport", Metadata: map[string]string{"iataCode": "LHR", "elevation": "eighty-three"}},
	})
	if err == nil || !strings.Contains(err.Error(), "elevation") {
		t.Fatalf("expected a type violation to be rejected, got %v", err)
	}
}

func TestValidateEntityInputsRejectsUndeclaredProperty(t *testing.T) {
	db := openOntologyTestDB(t)
	activateAviationSchema(t, db)

	err := db.validateEntityInputs(context.Background(), []ToolEntityInput{
		{Name: "London Heathrow", Type: "Airport", Metadata: map[string]string{"iataCode": "LHR", "runwayCount": "2"}},
	})
	if err == nil || !strings.Contains(err.Error(), "runwayCount") {
		t.Fatalf("expected an undeclared property to be rejected, got %v", err)
	}
}

func TestValidateEntityInputsNoopWithoutActiveSchema(t *testing.T) {
	db := openOntologyTestDB(t)

	err := db.validateEntityInputs(context.Background(), []ToolEntityInput{
		{Name: "Anything", Type: "Whatever"},
	})
	if err != nil {
		t.Fatalf("with no active schema all writes must pass, got %v", err)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./pkg/cortexdb -run TestValidateEntityInputs -v`
Expected: 除 `TestValidateEntityInputsNoopWithoutActiveSchema` 外全部 FAIL（桩恒返回 nil）。

- [ ] **Step 3: 写实现**

替换 `ontology_validation.go` 里的 `validateEntityInputs` 桩：

```go
// validateEntityInputs enforces the active ontology against entity writes.
// With no active schema every write is allowed, which is what keeps
// ontology-free deployments working unchanged.
func (db *DB) validateEntityInputs(ctx context.Context, entities []ToolEntityInput) error {
	compiled, err := db.activeCompiledOntology(ctx)
	if err != nil || compiled == nil {
		return err
	}
	for _, entity := range entities {
		if err := validateOntologyEntity(compiled, entity); err != nil {
			return err
		}
	}
	return nil
}

func (db *DB) activeCompiledOntology(ctx context.Context) (*compiledOntology, error) {
	schema, err := db.loadActiveOntologySchema(ctx)
	if err != nil || schema == nil {
		return nil, err
	}
	compiled := compileOntology(*schema)
	if compiled.isEmpty() {
		return nil, nil
	}
	return compiled, nil
}

func validateOntologyEntity(compiled *compiledOntology, entity ToolEntityInput) error {
	if strings.TrimSpace(entity.Name) == "" && strings.TrimSpace(entity.ID) == "" {
		return nil
	}

	objectTypeName := firstNonEmpty(entity.Type, "entity")
	objectType, ok := compiled.objectType(objectTypeName)
	if !ok {
		return fmt.Errorf("ontology does not define object type %q", objectTypeName)
	}

	if _, err := resolveOntologyPrimaryKeyValue(compiled, objectType.APIName, entity); err != nil {
		return err
	}

	supplied := make(map[string]string, len(entity.Metadata))
	for key, value := range entity.Metadata {
		supplied[ontologyAPIKey(key)] = value
	}

	for key, value := range supplied {
		property, ok := compiled.property(objectType.APIName, key)
		if !ok {
			return fmt.Errorf("object type %q has no property %q", objectType.APIName, key)
		}
		if err := parseOntologyPropertyValue(property.DataType, value); err != nil {
			return fmt.Errorf("object type %q property %q: %w", objectType.APIName, property.APIName, err)
		}
	}

	for _, property := range objectType.Properties {
		if !property.Required {
			continue
		}
		if ontologyAPIKey(property.APIName) == ontologyAPIKey(objectType.PrimaryKey) {
			// Already resolved above, and may legitimately arrive as the name.
			continue
		}
		if _, ok := supplied[ontologyAPIKey(property.APIName)]; !ok {
			return fmt.Errorf("object type %q is missing required property %q", objectType.APIName, property.APIName)
		}
	}
	return nil
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./pkg/cortexdb -run TestValidateEntityInputs -v`
Expected: PASS ×7。

- [ ] **Step 5: Commit**

```bash
git add pkg/cortexdb/ontology_validation.go pkg/cortexdb/ontology_write_validation_test.go
git commit -m "feat(ontology): validate entity writes against typed object types"
```

---

### Task 8: 关系写入准入校验 + 基数

**Files:**
- Modify: `pkg/cortexdb/ontology_validation.go`（替换 `validateRelationInputs` 桩）
- Test: `pkg/cortexdb/ontology_write_validation_test.go`（追加）

关系校验有一个第 1 期发现的老问题要一并修掉：v1 的实现要求两个端点**已经在图里**才能查到类型，同一批先建点再建边就会报 "could not resolve"。v2 改为：先查图，查不到再回退到本批次里同时提交的实体。

- [ ] **Step 1: 写失败的测试**

追加到 `ontology_write_validation_test.go`：

```go
func TestValidateRelationInputsAcceptsConformingRelation(t *testing.T) {
	db := openOntologyTestDB(t)
	activateAviationSchema(t, db)
	ctx := context.Background()

	tools := db.GraphRAGTools()
	if _, err := tools.UpsertEntities(ctx, ToolUpsertEntitiesRequest{Entities: []ToolEntityInput{
		{Name: "London Heathrow", Type: "Airport", Metadata: map[string]string{"iataCode": "LHR"}},
		{Name: "BA117", Type: "Flight", Metadata: map[string]string{"flightNumber": "BA117"}},
	}}); err != nil {
		t.Fatalf("upsert entities: %v", err)
	}

	err := db.validateRelationInputs(ctx, []ToolRelationInput{
		{From: "London Heathrow", To: "BA117", Type: "flightDeparture"},
	})
	if err != nil {
		t.Fatalf("expected conforming relation to pass, got %v", err)
	}
}

func TestValidateRelationInputsRejectsUnknownLinkType(t *testing.T) {
	db := openOntologyTestDB(t)
	activateAviationSchema(t, db)
	ctx := context.Background()

	err := db.validateRelationInputs(ctx, []ToolRelationInput{
		{From: "London Heathrow", To: "BA117", Type: "refuels"},
	})
	if err == nil || !strings.Contains(err.Error(), "refuels") {
		t.Fatalf("expected unknown link type to be rejected, got %v", err)
	}
}

func TestValidateRelationInputsRejectsWrongEndpointTypes(t *testing.T) {
	db := openOntologyTestDB(t)
	activateAviationSchema(t, db)
	ctx := context.Background()

	tools := db.GraphRAGTools()
	if _, err := tools.UpsertEntities(ctx, ToolUpsertEntitiesRequest{Entities: []ToolEntityInput{
		{Name: "London Heathrow", Type: "Airport", Metadata: map[string]string{"iataCode": "LHR"}},
		{Name: "Gatwick", Type: "Airport", Metadata: map[string]string{"iataCode": "LGW"}},
	}}); err != nil {
		t.Fatalf("upsert entities: %v", err)
	}

	err := db.validateRelationInputs(ctx, []ToolRelationInput{
		{From: "London Heathrow", To: "Gatwick", Type: "flightDeparture"},
	})
	if err == nil {
		t.Fatal("expected Airport->Airport on a flightDeparture link to be rejected")
	}
}

func TestValidateRelationInputsEnforcesOneCardinality(t *testing.T) {
	db := openOntologyTestDB(t)
	activateAviationSchema(t, db)
	ctx := context.Background()

	tools := db.GraphRAGTools()
	if _, err := tools.UpsertEntities(ctx, ToolUpsertEntitiesRequest{Entities: []ToolEntityInput{
		{Name: "London Heathrow", Type: "Airport", Metadata: map[string]string{"iataCode": "LHR"}},
		{Name: "Gatwick", Type: "Airport", Metadata: map[string]string{"iataCode": "LGW"}},
		{Name: "BA117", Type: "Flight", Metadata: map[string]string{"flightNumber": "BA117"}},
	}}); err != nil {
		t.Fatalf("upsert entities: %v", err)
	}
	if _, err := tools.UpsertRelations(ctx, ToolUpsertRelationsRequest{Relations: []ToolRelationInput{
		{From: "London Heathrow", To: "BA117", Type: "flightDeparture"},
	}}); err != nil {
		t.Fatalf("first relation: %v", err)
	}

	// BA117's origin side has cardinality ONE, so a second origin airport
	// must be refused rather than silently producing two origins.
	err := db.validateRelationInputs(ctx, []ToolRelationInput{
		{From: "Gatwick", To: "BA117", Type: "flightDeparture"},
	})
	if err == nil || !strings.Contains(err.Error(), "cardinality") {
		t.Fatalf("expected a cardinality violation, got %v", err)
	}
}

func TestValidateRelationInputsResolvesEndpointsFromSameBatch(t *testing.T) {
	db := openOntologyTestDB(t)
	activateAviationSchema(t, db)
	ctx := context.Background()

	// Neither endpoint exists in the graph yet. v1 failed here with
	// "could not resolve"; v2 must resolve from the batch.
	err := db.validateExtractedGraphData(ctx,
		map[string]GraphEntity{
			"entity:airport:lhr":   {Name: "London Heathrow", Type: "Airport"},
			"entity:flight:ba117":  {Name: "BA117", Type: "Flight"},
		},
		map[string]graph.GraphEdge{
			"e1": {FromNodeID: "entity:airport:lhr", ToNodeID: "entity:flight:ba117", EdgeType: "flightDeparture"},
		})
	if err != nil {
		t.Fatalf("expected same-batch endpoints to resolve, got %v", err)
	}
}
```

测试文件顶部 import 需加 `"github.com/liliang-cn/cortexdb/v2/pkg/graph"`。

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./pkg/cortexdb -run TestValidateRelationInputs -v`
Expected: 大部分 FAIL（桩恒返回 nil）。

- [ ] **Step 3: 写实现**

替换 `ontology_validation.go` 里的 `validateRelationInputs` 与 `validateExtractedGraphData` 桩：

```go
// ontologyTypeResolver maps a node ID to its object type API name.
type ontologyTypeResolver func(context.Context, []string) (map[string]string, error)

func (db *DB) validateRelationInputs(ctx context.Context, relations []ToolRelationInput) error {
	return db.validateRelationInputsWithResolver(ctx, relations, db.loadOntologyNodeTypes)
}

func (db *DB) validateRelationInputsWithResolver(ctx context.Context, relations []ToolRelationInput, resolve ontologyTypeResolver) error {
	compiled, err := db.activeCompiledOntology(ctx)
	if err != nil || compiled == nil {
		return err
	}

	nodeIDSet := make(map[string]struct{}, len(relations)*2)
	for _, relation := range relations {
		for _, endpoint := range []string{relation.From, relation.To} {
			if nodeID := resolveEntityNodeID("", endpoint); nodeID != "" {
				nodeIDSet[nodeID] = struct{}{}
			}
		}
	}
	nodeTypes, err := resolve(ctx, sortedKeysFromSet(nodeIDSet))
	if err != nil {
		return err
	}

	for _, relation := range relations {
		fromID := resolveEntityNodeID("", relation.From)
		toID := resolveEntityNodeID("", relation.To)
		if fromID == "" || toID == "" {
			return fmt.Errorf("relation endpoints are required")
		}

		linkTypeName := firstNonEmpty(relation.Type, "related_to")
		linkType, ok := compiled.linkType(linkTypeName)
		if !ok {
			return fmt.Errorf("ontology does not define link type %q", linkTypeName)
		}

		fromType, ok := nodeTypes[fromID]
		if !ok {
			return fmt.Errorf("ontology validation could not resolve source entity %s", relation.From)
		}
		toType, ok := nodeTypes[toID]
		if !ok {
			return fmt.Errorf("ontology validation could not resolve target entity %s", relation.To)
		}

		fromSide, toSide, err := compiled.orientLink(linkType, fromType, toType)
		if err != nil {
			return err
		}

		for key, value := range relation.Metadata {
			_ = key
			_ = value
		}

		if err := db.checkOntologyCardinality(ctx, linkType, fromID, fromSide, toID, toSide); err != nil {
			return err
		}
	}
	return nil
}

// checkOntologyCardinality refuses a second edge into a side declared ONE.
// The check runs against edges already in the graph; a batch that violates
// cardinality within itself is caught on the second relation.
func (db *DB) checkOntologyCardinality(ctx context.Context, linkType OntologyLinkType, fromID string, fromSide OntologyLinkSide, toID string, toSide OntologyLinkSide) error {
	// An edge from A to B occupies one slot on B's side as seen from A, and
	// vice versa. A ONE side means the *other* endpoint may hold at most one
	// edge of this link type.
	if toSide.Cardinality == OntologyCardinalityOne {
		count, err := db.countOntologyLinks(ctx, linkType.APIName, toID, fromID)
		if err != nil {
			return err
		}
		if count > 0 {
			return fmt.Errorf("link type %q side %q has cardinality ONE: %s already has a %s link",
				linkType.APIName, toSide.APIName, toID, linkType.APIName)
		}
	}
	if fromSide.Cardinality == OntologyCardinalityOne {
		count, err := db.countOntologyLinks(ctx, linkType.APIName, fromID, toID)
		if err != nil {
			return err
		}
		if count > 0 {
			return fmt.Errorf("link type %q side %q has cardinality ONE: %s already has a %s link",
				linkType.APIName, fromSide.APIName, fromID, linkType.APIName)
		}
	}
	return nil
}

// countOntologyLinks counts edges of a link type touching nodeID, excluding
// edges to excludeNodeID so that re-asserting the same link is idempotent.
func (db *DB) countOntologyLinks(ctx context.Context, linkTypeAPIName string, nodeID string, excludeNodeID string) (int, error) {
	row := db.store.GetDB().QueryRowContext(ctx, `
		SELECT COUNT(*) FROM graph_edges
		WHERE edge_type = ?
		  AND (from_node_id = ? OR to_node_id = ?)
		  AND from_node_id <> ? AND to_node_id <> ?
	`, linkTypeAPIName, nodeID, nodeID, excludeNodeID, excludeNodeID)

	var count int
	if err := row.Scan(&count); err != nil {
		return 0, fmt.Errorf("count ontology links: %w", err)
	}
	return count, nil
}

func (db *DB) loadOntologyNodeTypes(ctx context.Context, nodeIDs []string) (map[string]string, error) {
	nodes, err := db.graph.GetNodesBatch(ctx, nodeIDs)
	if err != nil {
		return nil, fmt.Errorf("load ontology validation nodes: %w", err)
	}
	nodeTypes := make(map[string]string, len(nodes))
	for _, node := range nodes {
		if node == nil {
			continue
		}
		nodeTypes[node.ID] = node.NodeType
	}
	return nodeTypes, nil
}

func (db *DB) validateExtractedGraphData(ctx context.Context, entities map[string]GraphEntity, relationships map[string]graph.GraphEdge) error {
	if len(entities) == 0 && len(relationships) == 0 {
		return nil
	}

	entityInputs := make([]ToolEntityInput, 0, len(entities))
	batchTypes := make(map[string]string, len(entities))
	for entityID, entity := range entities {
		objectType := firstNonEmpty(entity.Type, "entity")
		entityInputs = append(entityInputs, ToolEntityInput{ID: entityID, Name: entity.Name, Type: objectType})
		batchTypes[entityID] = objectType
	}
	if err := db.validateEntityInputs(ctx, entityInputs); err != nil {
		return err
	}

	relationInputs := make([]ToolRelationInput, 0, len(relationships))
	for _, relation := range relationships {
		relationInputs = append(relationInputs, ToolRelationInput{
			From:     relation.FromNodeID,
			To:       relation.ToNodeID,
			Type:     relation.EdgeType,
			Metadata: anyMapToStringMap(relation.Properties),
		})
	}

	// Resolve from the graph first, then fall back to types declared in this
	// same batch. v1 only did the former, so a batch that created both a node
	// and an edge in one call failed on the edge.
	return db.validateRelationInputsWithResolver(ctx, relationInputs, func(ctx context.Context, nodeIDs []string) (map[string]string, error) {
		resolved, err := db.loadOntologyNodeTypes(ctx, nodeIDs)
		if err != nil {
			return nil, err
		}
		for _, nodeID := range nodeIDs {
			if _, ok := resolved[nodeID]; ok {
				continue
			}
			if objectType, ok := batchTypes[nodeID]; ok {
				resolved[nodeID] = objectType
			}
		}
		return resolved, nil
	})
}
```

删除 `validateRelationInputs` 里那段空的 `for key, value := range relation.Metadata` 循环——它是占位，关系属性校验在下一步补。

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./pkg/cortexdb -run TestValidateRelationInputs -v && go test -race ./pkg/cortexdb`
Expected: PASS ×5，全包测试通过。

若 `graph_edges` 表名或列名与实际不符，先跑 `grep -n "CREATE TABLE.*graph_edges" -A 12 pkg/graph/graph.go` 核对后再改 `countOntologyLinks` 的 SQL。

- [ ] **Step 5: Commit**

```bash
git add pkg/cortexdb/ontology_validation.go pkg/cortexdb/ontology_write_validation_test.go
git commit -m "feat(ontology): validate relation writes, enforce per-side cardinality

Also fixes a v1 gap: endpoints created in the same batch as the edge now
resolve, instead of failing with 'could not resolve'."
```

---

### Task 9: 让写入路径使用主键身份

**Files:**
- Modify: `pkg/cortexdb/graphrag_tool_ingest.go:152`（`UpsertEntities` 内）
- Modify: `pkg/cortexdb/graphrag_tool_ingest.go:296`（`UpsertRelations` 内）
- Test: `pkg/cortexdb/ontology_identity_integration_test.go`

- [ ] **Step 1: 写失败的测试**

```go
package cortexdb

import (
	"context"
	"testing"
)

func TestUpsertEntitiesUsesPrimaryKeyIdentity(t *testing.T) {
	db := openOntologyTestDB(t)
	activateAviationSchema(t, db)
	ctx := context.Background()
	tools := db.GraphRAGTools()

	// Same airport, two different spellings of the name, one primary key.
	if _, err := tools.UpsertEntities(ctx, ToolUpsertEntitiesRequest{Entities: []ToolEntityInput{
		{Name: "London Heathrow", Type: "Airport", Metadata: map[string]string{"iataCode": "LHR"}},
	}}); err != nil {
		t.Fatalf("first upsert: %v", err)
	}
	if _, err := tools.UpsertEntities(ctx, ToolUpsertEntitiesRequest{Entities: []ToolEntityInput{
		{Name: "Heathrow Airport", Type: "Airport", Metadata: map[string]string{"iataCode": "LHR"}},
	}}); err != nil {
		t.Fatalf("second upsert: %v", err)
	}

	node, err := db.graph.GetNode(ctx, ontologyNodeID("Airport", "LHR"))
	if err != nil {
		t.Fatalf("expected a node at the primary-key ID: %v", err)
	}
	if node.NodeType != "Airport" {
		t.Fatalf("expected node type Airport, got %q", node.NodeType)
	}

	// Two names, one object: the second write must not have created a
	// second node.
	var count int
	row := db.store.GetDB().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM graph_nodes WHERE node_type = 'Airport'`)
	if err := row.Scan(&count); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected primary key to dedupe to 1 node, got %d", count)
	}
}

func TestUpsertEntitiesKeepsLegacyIdentityWithoutSchema(t *testing.T) {
	db := openOntologyTestDB(t)
	ctx := context.Background()
	tools := db.GraphRAGTools()

	if _, err := tools.UpsertEntities(ctx, ToolUpsertEntitiesRequest{Entities: []ToolEntityInput{
		{Name: "London Heathrow", Type: "airport"},
	}}); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	// With no active ontology the old name-derived ID must still be used,
	// so existing graphs keep resolving.
	if _, err := db.graph.GetNode(ctx, graphEntityNodeID("London Heathrow")); err != nil {
		t.Fatalf("expected legacy node ID to be preserved: %v", err)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./pkg/cortexdb -run 'TestUpsertEntitiesUsesPrimaryKeyIdentity|TestUpsertEntitiesKeepsLegacyIdentity' -v`
Expected: 第一个 FAIL（节点落在 `entity:london_heathrow`，且存在 2 个节点），第二个 PASS。

- [ ] **Step 3: 写实现**

在 `pkg/cortexdb/ontology_identity.go` 追加一个供写入路径调用的解析器：

```go
// ontologyEntityNodeID returns the node ID an entity should be written to.
// With an active ontology this is objectType+primaryKey; without one it
// falls back to the legacy name-derived ID so existing graphs keep working.
func ontologyEntityNodeID(compiled *compiledOntology, entity ToolEntityInput) (string, error) {
	if compiled == nil {
		return resolveEntityNodeID(entity.ID, entity.Name), nil
	}
	objectType, ok := compiled.objectType(firstNonEmpty(entity.Type, "entity"))
	if !ok {
		return "", fmt.Errorf("ontology does not define object type %q", entity.Type)
	}
	primaryKeyValue, err := resolveOntologyPrimaryKeyValue(compiled, objectType.APIName, entity)
	if err != nil {
		return "", err
	}
	return ontologyNodeID(objectType.APIName, primaryKeyValue), nil
}
```

在 `graphrag_tool_ingest.go` 的 `UpsertEntities` 里，把校验调用点（原 152 行）改成同时拿到编译后的本体，并在后续每处 `resolveEntityNodeID(entity.ID, entity.Name)` 改用 `ontologyEntityNodeID`：

```go
compiled, err := t.db.activeCompiledOntology(ctx)
if err != nil {
	return nil, err
}
if err := t.db.validateEntityInputs(ctx, req.Entities); err != nil {
	return nil, err
}
```

然后把该函数体内所有 `resolveEntityNodeID(entity.ID, entity.Name)` 替换为：

```go
nodeID, err := ontologyEntityNodeID(compiled, entity)
if err != nil {
	return nil, err
}
```

`UpsertRelations`（原 296 行）同理：先取 `compiled`，端点解析改用 `ontologyEntityNodeID(compiled, ToolEntityInput{Name: relation.From})`。注意 `From`/`To` 只有名字没有类型和元数据，所以当 `compiled != nil` 时必须先按名字查图拿到已存在的节点；查不到才报错。实现为：

```go
// ontologyRelationEndpointNodeID resolves a relation endpoint. Endpoints are
// referenced by name or ID only, so with an active ontology the node must
// already exist — its primary key is not recoverable from a name alone.
func (db *DB) ontologyRelationEndpointNodeID(ctx context.Context, compiled *compiledOntology, endpoint string) (string, error) {
	if compiled == nil {
		return resolveEntityNodeID("", endpoint), nil
	}
	if strings.HasPrefix(endpoint, "entity:") {
		return endpoint, nil
	}
	nodeID, ok, err := db.findOntologyNodeByName(ctx, endpoint)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", fmt.Errorf("relation endpoint %q does not resolve to an existing object; create it first or reference it by node ID", endpoint)
	}
	return nodeID, nil
}

func (db *DB) findOntologyNodeByName(ctx context.Context, name string) (string, bool, error) {
	row := db.store.GetDB().QueryRowContext(ctx,
		`SELECT id FROM graph_nodes WHERE content = ? LIMIT 1`, name)
	var nodeID string
	switch err := row.Scan(&nodeID); {
	case errors.Is(err, sql.ErrNoRows):
		return "", false, nil
	case err != nil:
		return "", false, fmt.Errorf("resolve endpoint by name: %w", err)
	default:
		return nodeID, true, nil
	}
}
```

放在 `ontology_identity.go`，文件顶部补 import `"context"`、`"database/sql"`、`"errors"`。

同时把 Task 8 的 `validateRelationInputsWithResolver` 里的 `resolveEntityNodeID("", endpoint)` 换成同一个解析器，保证校验和写入看到同一个节点 ID。

- [ ] **Step 4: 跑测试确认通过**

Run: `go test -race ./pkg/cortexdb`
Expected: 全部 PASS。若 `graph_nodes.content` 不是存名字的列，跑 `grep -n "CREATE TABLE.*graph_nodes" -A 12 pkg/graph/graph.go` 核对后改 `findOntologyNodeByName`。

- [ ] **Step 5: Commit 第 2 期**

```bash
git add pkg/cortexdb/ontology_identity.go pkg/cortexdb/graphrag_tool_ingest.go \
        pkg/cortexdb/ontology_validation.go pkg/cortexdb/ontology_identity_integration_test.go
git commit -m "feat(ontology): write entities at their primary-key identity

Objects with an active ontology now dedupe on objectType+primaryKey rather
than on name, so two spellings of one airport stop becoming two nodes."
```

---

# 第 3 期：Interfaces（多态）

目标：`find_nodes(type: "Facility")` 能召回所有实现 `Facility` 的对象类型（`Airport`、`Plant`、`Hangar`）。这是本体论对 GraphRAG 检索最直接的增益——agent 按抽象概念召回，不必知道具体类型名。

### Task 10: 接口继承闭包与实现校验

**Files:**
- Create: `pkg/cortexdb/ontology_interface.go`
- Modify: `pkg/cortexdb/ontology_validation.go`（`validateOntologySchema` 里加接口约束校验）
- Modify: `pkg/cortexdb/ontology_compile.go`（`compiledOntology` 加闭包字段）
- Test: `pkg/cortexdb/ontology_interface_test.go`

- [ ] **Step 1: 写失败的测试**

```go
package cortexdb

import (
	"strings"
	"testing"
)

func facilitySchema() OntologySchema {
	return OntologySchema{
		SchemaID: "facilities",
		Name:     "Facilities",
		InterfaceTypes: []OntologyInterfaceType{
			{
				APIName: "Locatable",
				Properties: []OntologyProperty{
					{APIName: "position", DataType: OntologyDataType{Kind: OntologyDataGeoPoint}, Required: true},
				},
			},
			{
				APIName: "Facility",
				Extends: []string{"Locatable"},
				Properties: []OntologyProperty{
					{APIName: "facilityName", DataType: OntologyDataType{Kind: OntologyDataString}, Required: true},
					{APIName: "capacity", DataType: OntologyDataType{Kind: OntologyDataInteger}},
				},
			},
		},
		ObjectTypes: []OntologyObjectType{
			{
				APIName:    "Airport",
				PrimaryKey: "iataCode",
				Implements: []string{"Facility"},
				Properties: []OntologyProperty{
					{APIName: "iataCode", DataType: OntologyDataType{Kind: OntologyDataString}, Required: true},
					{APIName: "facilityName", DataType: OntologyDataType{Kind: OntologyDataString}, Required: true},
					{APIName: "position", DataType: OntologyDataType{Kind: OntologyDataGeoPoint}, Required: true},
				},
			},
			{
				APIName:    "Plant",
				PrimaryKey: "plantCode",
				Implements: []string{"Facility"},
				Properties: []OntologyProperty{
					{APIName: "plantCode", DataType: OntologyDataType{Kind: OntologyDataString}, Required: true},
					{APIName: "facilityName", DataType: OntologyDataType{Kind: OntologyDataString}, Required: true},
					{APIName: "position", DataType: OntologyDataType{Kind: OntologyDataGeoPoint}, Required: true},
				},
			},
			{
				APIName:    "Warehouse",
				PrimaryKey: "warehouseCode",
				Properties: []OntologyProperty{
					{APIName: "warehouseCode", DataType: OntologyDataType{Kind: OntologyDataString}, Required: true},
				},
			},
		},
	}
}

func TestInterfaceClosureIncludesInheritedInterfaces(t *testing.T) {
	compiled := compileOntology(facilitySchema())

	closure := compiled.interfaceClosure("Facility")
	if len(closure) != 2 {
		t.Fatalf("expected Facility and Locatable, got %v", closure)
	}
	if _, ok := closure[ontologyAPIKey("Locatable")]; !ok {
		t.Fatalf("Facility extends Locatable, closure was %v", closure)
	}
}

func TestImplementingObjectTypesResolvesThroughInheritance(t *testing.T) {
	compiled := compileOntology(facilitySchema())

	implementors := compiled.implementingObjectTypes("Locatable")
	if len(implementors) != 2 {
		t.Fatalf("expected Airport and Plant to implement Locatable transitively, got %v", implementors)
	}

	direct := compiled.implementingObjectTypes("Facility")
	if len(direct) != 2 {
		t.Fatalf("expected 2 Facility implementors, got %v", direct)
	}
	if len(compiled.implementingObjectTypes("Nonexistent")) != 0 {
		t.Fatal("unknown interface must resolve to no implementors")
	}
}

func TestResolveTypeClosureExpandsInterfacesButNotObjectTypes(t *testing.T) {
	compiled := compileOntology(facilitySchema())

	byInterface := compiled.resolveTypeClosure("Facility")
	if len(byInterface) != 2 {
		t.Fatalf("interface must expand to its implementors, got %v", byInterface)
	}

	byObjectType := compiled.resolveTypeClosure("Airport")
	if len(byObjectType) != 1 || byObjectType[0] != "Airport" {
		t.Fatalf("object type must resolve to itself, got %v", byObjectType)
	}

	unknown := compiled.resolveTypeClosure("Spacecraft")
	if len(unknown) != 1 || unknown[0] != "Spacecraft" {
		t.Fatalf("unknown type passes through unchanged, got %v", unknown)
	}
}

func TestValidateSchemaRejectsInterfaceCycle(t *testing.T) {
	schema := facilitySchema()
	schema.InterfaceTypes[0].Extends = []string{"Facility"} // Locatable -> Facility -> Locatable

	err := validateOntologySchema(schema)
	if err == nil || !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("expected an interface cycle to be rejected, got %v", err)
	}
}

func TestValidateSchemaRequiresImplementorsToSatisfyRequiredProperties(t *testing.T) {
	schema := facilitySchema()
	// Drop the position property Airport needs to satisfy Locatable.
	schema.ObjectTypes[0].Properties = schema.ObjectTypes[0].Properties[:2]

	err := validateOntologySchema(schema)
	if err == nil || !strings.Contains(err.Error(), "position") {
		t.Fatalf("expected an unsatisfied interface property to be rejected, got %v", err)
	}
}

func TestValidateSchemaRequiresImplementorPropertyTypesToMatch(t *testing.T) {
	schema := facilitySchema()
	schema.ObjectTypes[0].Properties[2].DataType = OntologyDataType{Kind: OntologyDataString}

	err := validateOntologySchema(schema)
	if err == nil || !strings.Contains(err.Error(), "position") {
		t.Fatalf("expected a data type mismatch against the interface to be rejected, got %v", err)
	}
}

func TestValidateSchemaAllowsOptionalInterfacePropertyToBeSkipped(t *testing.T) {
	// capacity is optional on Facility and declared by neither implementor.
	if err := validateOntologySchema(facilitySchema()); err != nil {
		t.Fatalf("optional interface properties may be skipped, got %v", err)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./pkg/cortexdb -run 'TestInterfaceClosure|TestImplementingObjectTypes|TestResolveTypeClosure|TestValidateSchemaRejectsInterfaceCycle|TestValidateSchemaRequiresImplementor|TestValidateSchemaAllowsOptional' -v`
Expected: `undefined: (*compiledOntology).interfaceClosure`。

- [ ] **Step 3: 写实现**

新建 `pkg/cortexdb/ontology_interface.go`：

```go
package cortexdb

import (
	"fmt"
	"sort"
)

// interfaceClosure returns the API keys of an interface and every interface
// it extends, transitively. Foundry allows an interface to extend several
// parents, and object types to implement several interfaces, so a plain
// parent pointer is not enough.
func (c *compiledOntology) interfaceClosure(apiName string) map[string]struct{} {
	closure := make(map[string]struct{})
	c.collectInterfaceClosure(apiName, closure)
	return closure
}

func (c *compiledOntology) collectInterfaceClosure(apiName string, closure map[string]struct{}) {
	key := ontologyAPIKey(apiName)
	if _, seen := closure[key]; seen {
		return
	}
	interfaceType, ok := c.interfaceType(apiName)
	if !ok {
		return
	}
	closure[key] = struct{}{}
	for _, parent := range interfaceType.Extends {
		c.collectInterfaceClosure(parent, closure)
	}
}

// implementingObjectTypes returns the declared API names of every object type
// that implements the interface, directly or through interface inheritance.
func (c *compiledOntology) implementingObjectTypes(interfaceAPIName string) []string {
	if _, ok := c.interfaceType(interfaceAPIName); !ok {
		return nil
	}
	target := ontologyAPIKey(interfaceAPIName)

	names := make([]string, 0, len(c.objectTypes))
	for _, objectType := range c.objectTypes {
		for _, implemented := range objectType.Implements {
			if _, ok := c.interfaceClosure(implemented)[target]; ok {
				names = append(names, objectType.APIName)
				break
			}
		}
	}
	sort.Strings(names)
	return names
}

// resolveTypeClosure turns a type name used in a query into the concrete
// object type names it should match. An interface expands to its
// implementors; anything else passes through unchanged so callers can keep
// querying by object type or by a legacy node type.
func (c *compiledOntology) resolveTypeClosure(typeAPIName string) []string {
	if _, ok := c.interfaceType(typeAPIName); ok {
		return c.implementingObjectTypes(typeAPIName)
	}
	if objectType, ok := c.objectType(typeAPIName); ok {
		return []string{objectType.APIName}
	}
	return []string{typeAPIName}
}

// validateOntologyInterfaces checks the interface graph is acyclic and that
// every implementor actually satisfies the shape it claims.
func validateOntologyInterfaces(schema OntologySchema, compiled *compiledOntology) error {
	if err := checkInterfaceCycles(compiled); err != nil {
		return err
	}

	for _, objectType := range schema.ObjectTypes {
		required := map[string]OntologyProperty{}
		for _, implemented := range objectType.Implements {
			for key := range compiled.interfaceClosure(implemented) {
				interfaceType, ok := compiled.interfaces[key]
				if !ok {
					continue
				}
				for _, property := range interfaceType.Properties {
					if property.Required {
						required[ontologyAPIKey(property.APIName)] = property
					}
				}
			}
		}

		for key, interfaceProperty := range required {
			objectProperty, ok := compiled.property(objectType.APIName, key)
			if !ok {
				return fmt.Errorf("object type %q implements an interface requiring property %q but does not declare it",
					objectType.APIName, interfaceProperty.APIName)
			}
			if objectProperty.DataType.Kind != interfaceProperty.DataType.Kind {
				return fmt.Errorf("object type %q property %q is %q but the interface requires %q",
					objectType.APIName, interfaceProperty.APIName,
					objectProperty.DataType.Kind, interfaceProperty.DataType.Kind)
			}
		}
	}
	return nil
}

func checkInterfaceCycles(compiled *compiledOntology) error {
	const (
		unvisited = 0
		inStack   = 1
		done      = 2
	)
	state := make(map[string]int, len(compiled.interfaces))

	var visit func(key string) error
	visit = func(key string) error {
		switch state[key] {
		case inStack:
			return fmt.Errorf("interface inheritance cycle detected at %q", key)
		case done:
			return nil
		}
		state[key] = inStack
		if interfaceType, ok := compiled.interfaces[key]; ok {
			for _, parent := range interfaceType.Extends {
				if err := visit(ontologyAPIKey(parent)); err != nil {
					return err
				}
			}
		}
		state[key] = done
		return nil
	}

	keys := make([]string, 0, len(compiled.interfaces))
	for key := range compiled.interfaces {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if err := visit(key); err != nil {
			return err
		}
	}
	return nil
}
```

在 `ontology_validation.go` 的 `validateOntologySchema` **末尾**（`return nil` 之前）接上接口校验：

```go
	compiled := compileOntology(schema)
	if err := validateOntologyInterfaces(schema, compiled); err != nil {
		return err
	}
	return nil
}
```

注意：`validateOntologySchema` 已有的「object type implements unknown interface」检查要保留，它在 `compileOntology` 之前就能给出更清楚的报错。

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./pkg/cortexdb -run 'TestInterfaceClosure|TestImplementingObjectTypes|TestResolveTypeClosure|TestValidateSchema' -v`
Expected: PASS ×7。

- [ ] **Step 5: Commit**

```bash
git add pkg/cortexdb/ontology_interface.go pkg/cortexdb/ontology_validation.go \
        pkg/cortexdb/ontology_interface_test.go
git commit -m "feat(ontology): add interface types with inheritance and implementation checks"
```

---

### Task 11: 让检索按接口召回

**Files:**
- Modify: `pkg/cortexdb/graphrag_tool_find_nodes.go`（类型过滤前展开闭包）
- Test: `pkg/cortexdb/ontology_interface_retrieval_test.go`

- [ ] **Step 1: 写失败的测试**

```go
package cortexdb

import (
	"context"
	"testing"
)

func TestFindNodesByInterfaceReturnsAllImplementors(t *testing.T) {
	db := openOntologyTestDB(t)
	ctx := context.Background()

	if _, err := db.SaveOntologySchema(ctx, OntologySaveRequest{Schema: facilitySchema(), Activate: true}); err != nil {
		t.Fatalf("activate: %v", err)
	}

	tools := db.GraphRAGTools()
	if _, err := tools.UpsertEntities(ctx, ToolUpsertEntitiesRequest{Entities: []ToolEntityInput{
		{Name: "London Heathrow", Type: "Airport", Metadata: map[string]string{
			"iataCode": "LHR", "facilityName": "London Heathrow", "position": "51.4700,-0.4543"}},
		{Name: "Sunderland Plant", Type: "Plant", Metadata: map[string]string{
			"plantCode": "SUN", "facilityName": "Sunderland Plant", "position": "54.9060,-1.3830"}},
		{Name: "Depot 4", Type: "Warehouse", Metadata: map[string]string{"warehouseCode": "W4"}},
	}}); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	resp, err := tools.FindNodes(ctx, ToolFindNodesRequest{NodeTypes: []string{"Facility"}})
	if err != nil {
		t.Fatalf("find nodes: %v", err)
	}
	if len(resp.Nodes) != 2 {
		t.Fatalf("expected the 2 Facility implementors, got %d", len(resp.Nodes))
	}
	for _, node := range resp.Nodes {
		if node.NodeType == "Warehouse" {
			t.Fatal("Warehouse does not implement Facility and must not be returned")
		}
	}
}

func TestFindNodesByObjectTypeIsUnaffectedByInterfaces(t *testing.T) {
	db := openOntologyTestDB(t)
	ctx := context.Background()

	if _, err := db.SaveOntologySchema(ctx, OntologySaveRequest{Schema: facilitySchema(), Activate: true}); err != nil {
		t.Fatalf("activate: %v", err)
	}
	tools := db.GraphRAGTools()
	if _, err := tools.UpsertEntities(ctx, ToolUpsertEntitiesRequest{Entities: []ToolEntityInput{
		{Name: "London Heathrow", Type: "Airport", Metadata: map[string]string{
			"iataCode": "LHR", "facilityName": "London Heathrow", "position": "51.4700,-0.4543"}},
		{Name: "Sunderland Plant", Type: "Plant", Metadata: map[string]string{
			"plantCode": "SUN", "facilityName": "Sunderland Plant", "position": "54.9060,-1.3830"}},
	}}); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	resp, err := tools.FindNodes(ctx, ToolFindNodesRequest{NodeTypes: []string{"Airport"}})
	if err != nil {
		t.Fatalf("find nodes: %v", err)
	}
	if len(resp.Nodes) != 1 {
		t.Fatalf("expected only Airport, got %d", len(resp.Nodes))
	}
}
```

先跑 `grep -n "type ToolFindNodesRequest struct" -A 12 pkg/cortexdb/graphrag_tool_types.go` 确认字段名（可能是 `NodeTypes` 或 `Types`），并据此改测试。

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./pkg/cortexdb -run TestFindNodesByInterface -v`
Expected: `expected the 2 Facility implementors, got 0`——`Facility` 不是任何节点的 `node_type`。

- [ ] **Step 3: 写实现**

在 `pkg/cortexdb/ontology_interface.go` 追加一个供检索路径调用的展开器：

```go
// expandOntologyTypeFilter rewrites a caller-supplied list of type names into
// the concrete object types to match on, so a query for an interface hits
// every implementor. Without an active ontology the list passes through.
func (db *DB) expandOntologyTypeFilter(ctx context.Context, typeNames []string) ([]string, error) {
	if len(typeNames) == 0 {
		return typeNames, nil
	}
	compiled, err := db.activeCompiledOntology(ctx)
	if err != nil || compiled == nil {
		return typeNames, err
	}

	seen := make(map[string]struct{}, len(typeNames))
	expanded := make([]string, 0, len(typeNames))
	for _, typeName := range typeNames {
		for _, resolved := range compiled.resolveTypeClosure(typeName) {
			key := ontologyAPIKey(resolved)
			if _, exists := seen[key]; exists {
				continue
			}
			seen[key] = struct{}{}
			expanded = append(expanded, resolved)
		}
	}
	return expanded, nil
}
```

在 `graphrag_tool_find_nodes.go` 的 `FindNodes` 里，在把 `req.NodeTypes` 传给图查询之前插入：

```go
nodeTypes, err := t.db.expandOntologyTypeFilter(ctx, req.NodeTypes)
if err != nil {
	return nil, err
}
```

并把后续使用 `req.NodeTypes` 的地方改成 `nodeTypes`。

同样的展开也要加到 `expand_graph` 和 `search_chunks_by_entities` 的类型过滤路径——用 `grep -rn "NodeTypes" pkg/cortexdb/*.go | grep -v _test` 找齐所有入口，逐个接上。

- [ ] **Step 4: 跑测试确认通过**

Run: `go test -race ./pkg/cortexdb`
Expected: 全部 PASS。

- [ ] **Step 5: Commit 第 3 期**

```bash
git add pkg/cortexdb/ontology_interface.go pkg/cortexdb/graphrag_tool_find_nodes.go \
        pkg/cortexdb/ontology_interface_retrieval_test.go
git commit -m "feat(ontology): retrieve by interface, expanding to every implementor"
```

---

# 第 4 期：ObjectSet 代数

Foundry 的 ObjectSet 是一个递归 union 类型，`nearestNeighbors`（向量）、`containsAllTerms`（全文）、`searchAround`（图遍历）在其中是**平级的一等算子**。CortexDB 三个引擎都已具备，本期只是把它们统一到一个可组合表达式下面。

**求值策略**：`resolveObjectSet` 返回一个节点 ID 集合（`map[string]struct{}`）。集合运算在内存里做；`base`/`filter` 的标量谓词下推 SQL；文本谓词走 FTS5；向量谓词走已有向量索引；`searchAround` 走 `pkg/graph` 的遍历。**照抄 Foundry 的 searchAround 深度上限 3**。

### Task 12: ObjectSet 与 Predicate 类型

**Files:**
- Modify: `pkg/cortexdb/objectset_types.go`（替换第 1 期的占位）
- Test: `pkg/cortexdb/objectset_types_test.go`

- [ ] **Step 1: 写失败的测试**

```go
package cortexdb

import (
	"encoding/json"
	"testing"
)

func TestObjectSetJSONRoundTripNested(t *testing.T) {
	set := ObjectSet{
		Kind: ObjectSetIntersect,
		Operands: []ObjectSet{
			{
				Kind:   ObjectSetFilter,
				Source: &ObjectSet{Kind: ObjectSetBase, ObjectType: "Airport"},
				Where: &ObjectSetPredicate{
					Op: PredicateAnd,
					Operands: []ObjectSetPredicate{
						{Op: PredicateGte, Property: "capacity", Value: "1000"},
						{Op: PredicateContainsAllTerms, Property: "facilityName", Value: "london heathrow"},
					},
				},
			},
			{
				Kind:   ObjectSetSearchAround,
				Source: &ObjectSet{Kind: ObjectSetBase, ObjectType: "Flight"},
				Link:   "origin",
			},
		},
	}

	encoded, err := json.Marshal(set)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded ObjectSet
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if decoded.Operands[0].Source.ObjectType != "Airport" {
		t.Fatalf("nested source lost: %+v", decoded.Operands[0].Source)
	}
	if len(decoded.Operands[0].Where.Operands) != 2 {
		t.Fatalf("nested predicate lost: %+v", decoded.Operands[0].Where)
	}
	if decoded.Operands[1].Link != "origin" {
		t.Fatalf("search around link lost: %q", decoded.Operands[1].Link)
	}
}

func TestValidateObjectSetRejectsMissingSource(t *testing.T) {
	if err := validateObjectSet(ObjectSet{Kind: ObjectSetFilter}, 0); err == nil {
		t.Fatal("filter without a source must be rejected")
	}
}

func TestValidateObjectSetRejectsBaseWithoutObjectType(t *testing.T) {
	if err := validateObjectSet(ObjectSet{Kind: ObjectSetBase}, 0); err == nil {
		t.Fatal("base without an object type must be rejected")
	}
}

func TestValidateObjectSetRejectsExcessSearchAroundDepth(t *testing.T) {
	// Four chained search-arounds; Foundry caps this at three.
	set := ObjectSet{Kind: ObjectSetBase, ObjectType: "Flight"}
	for i := 0; i < 4; i++ {
		source := set
		set = ObjectSet{Kind: ObjectSetSearchAround, Source: &source, Link: "origin"}
	}
	err := validateObjectSet(set, 0)
	if err == nil {
		t.Fatal("more than 3 chained search-arounds must be rejected")
	}
}

func TestValidateObjectSetAcceptsThreeSearchArounds(t *testing.T) {
	set := ObjectSet{Kind: ObjectSetBase, ObjectType: "Flight"}
	for i := 0; i < 3; i++ {
		source := set
		set = ObjectSet{Kind: ObjectSetSearchAround, Source: &source, Link: "origin"}
	}
	if err := validateObjectSet(set, 0); err != nil {
		t.Fatalf("3 search-arounds is the documented limit, got %v", err)
	}
}

func TestValidateObjectSetRejectsEmptySetOperation(t *testing.T) {
	if err := validateObjectSet(ObjectSet{Kind: ObjectSetUnion}, 0); err == nil {
		t.Fatal("union with no operands must be rejected")
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./pkg/cortexdb -run 'TestObjectSetJSONRoundTrip|TestValidateObjectSet' -v`
Expected: `undefined: ObjectSetIntersect`。

- [ ] **Step 3: 写实现**

清空 `pkg/cortexdb/objectset_types.go` 并写入：

```go
package cortexdb

import "fmt"

// maxSearchAroundDepth mirrors Foundry's documented runtime limit of three
// chained search-arounds per query. Beyond that the traversal fan-out stops
// being something a single request should carry.
const maxSearchAroundDepth = 3

// ObjectSetKind is the discriminator of the ObjectSet union type.
type ObjectSetKind string

const (
	ObjectSetBase          ObjectSetKind = "base"
	ObjectSetInterfaceBase ObjectSetKind = "interface_base"
	ObjectSetStatic        ObjectSetKind = "static"
	ObjectSetFilter        ObjectSetKind = "filter"
	ObjectSetUnion         ObjectSetKind = "union"
	ObjectSetIntersect     ObjectSetKind = "intersect"
	ObjectSetSubtract      ObjectSetKind = "subtract"
	ObjectSetSearchAround  ObjectSetKind = "search_around"
	ObjectSetReference     ObjectSetKind = "reference"
)

// PredicateOp is the discriminator of a filter predicate.
type PredicateOp string

const (
	PredicateEq               PredicateOp = "eq"
	PredicateLt               PredicateOp = "lt"
	PredicateLte              PredicateOp = "lte"
	PredicateGt               PredicateOp = "gt"
	PredicateGte              PredicateOp = "gte"
	PredicateIsNull           PredicateOp = "is_null"
	PredicateIn               PredicateOp = "in"
	PredicateContains         PredicateOp = "contains"
	PredicateStartsWith       PredicateOp = "starts_with"
	PredicateContainsAllTerms PredicateOp = "contains_all_terms"
	PredicateContainsAnyTerm  PredicateOp = "contains_any_term"
	PredicateNearestNeighbors PredicateOp = "nearest_neighbors"
	PredicateAnd              PredicateOp = "and"
	PredicateOr               PredicateOp = "or"
	PredicateNot              PredicateOp = "not"
)

// ObjectSetPredicate is the filter expression tree.
type ObjectSetPredicate struct {
	Op       PredicateOp          `json:"op"`
	Property string               `json:"property,omitempty"`
	Value    string               `json:"value,omitempty"`
	Values   []string             `json:"values,omitempty"`
	Operands []ObjectSetPredicate `json:"operands,omitempty"`
	// K bounds a nearest_neighbors predicate. Foundry caps this at 100.
	K int `json:"k,omitempty"`
	// Vector is the query vector for nearest_neighbors. When empty the
	// resolver embeds Value instead, which is what agents will normally send.
	Vector []float32 `json:"vector,omitempty"`
}

// ObjectSet is a composable description of a set of objects. It is the one
// place where vector search, full-text search and graph traversal are peers
// rather than three separate APIs.
type ObjectSet struct {
	Kind ObjectSetKind `json:"kind"`

	// base
	ObjectType string `json:"object_type,omitempty"`
	// interface_base
	InterfaceType string `json:"interface_type,omitempty"`
	// static
	ObjectIDs []string `json:"object_ids,omitempty"`
	// reference — a saved object set on the active schema
	Reference string `json:"reference,omitempty"`
	// filter / search_around
	Source *ObjectSetPredicateSource `json:"-"`
	Where  *ObjectSetPredicate       `json:"where,omitempty"`
	// search_around: the link *side* api name to traverse
	Link string `json:"link,omitempty"`
	// union / intersect / subtract
	Operands []ObjectSet `json:"operands,omitempty"`
}

// OntologyNamedObjectSet is a saved, reusable object set definition.
type OntologyNamedObjectSet struct {
	APIName     string    `json:"api_name"`
	Description string    `json:"description,omitempty"`
	Definition  ObjectSet `json:"definition"`
}

func validateObjectSet(set ObjectSet, searchAroundDepth int) error {
	if searchAroundDepth > maxSearchAroundDepth {
		return fmt.Errorf("object set exceeds the %d search-around limit", maxSearchAroundDepth)
	}

	switch set.Kind {
	case ObjectSetBase:
		if set.ObjectType == "" {
			return fmt.Errorf("object set kind %q requires object_type", set.Kind)
		}
		return nil
	case ObjectSetInterfaceBase:
		if set.InterfaceType == "" {
			return fmt.Errorf("object set kind %q requires interface_type", set.Kind)
		}
		return nil
	case ObjectSetStatic:
		if len(set.ObjectIDs) == 0 {
			return fmt.Errorf("object set kind %q requires object_ids", set.Kind)
		}
		return nil
	case ObjectSetReference:
		if set.Reference == "" {
			return fmt.Errorf("object set kind %q requires reference", set.Kind)
		}
		return nil
	case ObjectSetFilter:
		if set.Source == nil {
			return fmt.Errorf("object set kind %q requires source", set.Kind)
		}
		if set.Where == nil {
			return fmt.Errorf("object set kind %q requires where", set.Kind)
		}
		if err := validateObjectSetPredicate(*set.Where); err != nil {
			return err
		}
		return validateObjectSet(ObjectSet(*set.Source), searchAroundDepth)
	case ObjectSetSearchAround:
		if set.Source == nil {
			return fmt.Errorf("object set kind %q requires source", set.Kind)
		}
		if set.Link == "" {
			return fmt.Errorf("object set kind %q requires link", set.Kind)
		}
		return validateObjectSet(ObjectSet(*set.Source), searchAroundDepth+1)
	case ObjectSetUnion, ObjectSetIntersect, ObjectSetSubtract:
		if len(set.Operands) < 2 {
			return fmt.Errorf("object set kind %q requires at least 2 operands", set.Kind)
		}
		for _, operand := range set.Operands {
			if err := validateObjectSet(operand, searchAroundDepth); err != nil {
				return err
			}
		}
		return nil
	default:
		return fmt.Errorf("unknown object set kind %q", set.Kind)
	}
}

func validateObjectSetPredicate(predicate ObjectSetPredicate) error {
	switch predicate.Op {
	case PredicateAnd, PredicateOr:
		if len(predicate.Operands) < 2 {
			return fmt.Errorf("predicate %q requires at least 2 operands", predicate.Op)
		}
	case PredicateNot:
		if len(predicate.Operands) != 1 {
			return fmt.Errorf("predicate %q requires exactly 1 operand", predicate.Op)
		}
	case PredicateIsNull:
		if predicate.Property == "" {
			return fmt.Errorf("predicate %q requires property", predicate.Op)
		}
		return nil
	case PredicateIn:
		if predicate.Property == "" || len(predicate.Values) == 0 {
			return fmt.Errorf("predicate %q requires property and values", predicate.Op)
		}
		return nil
	case PredicateNearestNeighbors:
		if predicate.Property == "" {
			return fmt.Errorf("predicate %q requires property", predicate.Op)
		}
		if predicate.Value == "" && len(predicate.Vector) == 0 {
			return fmt.Errorf("predicate %q requires value or vector", predicate.Op)
		}
		if predicate.K < 0 || predicate.K > 100 {
			return fmt.Errorf("predicate %q requires 0 < k <= 100", predicate.Op)
		}
		return nil
	case PredicateEq, PredicateLt, PredicateLte, PredicateGt, PredicateGte,
		PredicateContains, PredicateStartsWith, PredicateContainsAllTerms, PredicateContainsAnyTerm:
		if predicate.Property == "" || predicate.Value == "" {
			return fmt.Errorf("predicate %q requires property and value", predicate.Op)
		}
		return nil
	default:
		return fmt.Errorf("unknown predicate op %q", predicate.Op)
	}

	for _, operand := range predicate.Operands {
		if err := validateObjectSetPredicate(operand); err != nil {
			return err
		}
	}
	return nil
}
```

`ObjectSet` 递归引用自身需要一个间接层才能让 `encoding/json` 处理，加在同一文件末尾：

```go
// ObjectSetPredicateSource exists only to break the recursive type reference
// so encoding/json can generate a decoder for ObjectSet.
type ObjectSetPredicateSource ObjectSet

// MarshalJSON / UnmarshalJSON keep `source` in the wire format while the Go
// type carries the indirection.
func (s ObjectSet) MarshalJSON() ([]byte, error) {
	type alias ObjectSet
	return json.Marshal(struct {
		alias
		Source *ObjectSetPredicateSource `json:"source,omitempty"`
	}{alias(s), s.Source})
}

func (s *ObjectSet) UnmarshalJSON(data []byte) error {
	type alias ObjectSet
	var decoded struct {
		alias
		Source *ObjectSetPredicateSource `json:"source,omitempty"`
	}
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	*s = ObjectSet(decoded.alias)
	s.Source = decoded.Source
	return nil
}
```

文件顶部 import 改为 `import ("encoding/json"; "fmt")`。

> 实现者注意：`Source *ObjectSetPredicateSource` 这层间接是为了让自定义 Marshal/Unmarshal 不无限递归。若在实现时发现更简单的写法（例如直接用 `*ObjectSet` 并省掉自定义方法——Go 对指针型自引用其实是支持的），**优先用更简单的**，只要 `TestObjectSetJSONRoundTripNested` 通过即可。先跑一次不带自定义方法的版本验证。

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./pkg/cortexdb -run 'TestObjectSetJSONRoundTrip|TestValidateObjectSet' -v`
Expected: PASS ×6。

- [ ] **Step 5: Commit**

```bash
git add pkg/cortexdb/objectset_types.go pkg/cortexdb/objectset_types_test.go
git commit -m "feat(objectset): add the composable object set algebra and its validator"
```

---

### Task 13: 求值器 — base / static / 集合运算 / 标量谓词

**Files:**
- Create: `pkg/cortexdb/objectset_resolve.go`
- Test: `pkg/cortexdb/objectset_resolve_test.go`

- [ ] **Step 1: 写失败的测试**

```go
package cortexdb

import (
	"context"
	"sort"
	"testing"
)

func seedFacilities(t *testing.T, db *DB) {
	t.Helper()
	ctx := context.Background()

	schema := facilitySchema()
	schema.ObjectTypes[0].Properties = append(schema.ObjectTypes[0].Properties,
		OntologyProperty{APIName: "capacity", DataType: OntologyDataType{Kind: OntologyDataInteger}})
	schema.ObjectTypes[1].Properties = append(schema.ObjectTypes[1].Properties,
		OntologyProperty{APIName: "capacity", DataType: OntologyDataType{Kind: OntologyDataInteger}})

	if _, err := db.SaveOntologySchema(ctx, OntologySaveRequest{Schema: schema, Activate: true}); err != nil {
		t.Fatalf("activate: %v", err)
	}
	if _, err := db.GraphRAGTools().UpsertEntities(ctx, ToolUpsertEntitiesRequest{Entities: []ToolEntityInput{
		{Name: "London Heathrow", Type: "Airport", Metadata: map[string]string{
			"iataCode": "LHR", "facilityName": "London Heathrow", "position": "51.4700,-0.4543", "capacity": "80000"}},
		{Name: "Gatwick", Type: "Airport", Metadata: map[string]string{
			"iataCode": "LGW", "facilityName": "Gatwick", "position": "51.1537,-0.1821", "capacity": "40000"}},
		{Name: "Sunderland Plant", Type: "Plant", Metadata: map[string]string{
			"plantCode": "SUN", "facilityName": "Sunderland Plant", "position": "54.9060,-1.3830", "capacity": "5000"}},
	}}); err != nil {
		t.Fatalf("seed: %v", err)
	}
}

func resolvedIDs(t *testing.T, db *DB, set ObjectSet) []string {
	t.Helper()
	resolved, err := db.ResolveObjectSet(context.Background(), set)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	ids := make([]string, 0, len(resolved))
	for id := range resolved {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func TestResolveObjectSetBase(t *testing.T) {
	db := openOntologyTestDB(t)
	seedFacilities(t, db)

	ids := resolvedIDs(t, db, ObjectSet{Kind: ObjectSetBase, ObjectType: "Airport"})
	if len(ids) != 2 {
		t.Fatalf("expected 2 airports, got %v", ids)
	}
}

func TestResolveObjectSetInterfaceBase(t *testing.T) {
	db := openOntologyTestDB(t)
	seedFacilities(t, db)

	ids := resolvedIDs(t, db, ObjectSet{Kind: ObjectSetInterfaceBase, InterfaceType: "Facility"})
	if len(ids) != 3 {
		t.Fatalf("expected all 3 facilities, got %v", ids)
	}
}

func TestResolveObjectSetStatic(t *testing.T) {
	db := openOntologyTestDB(t)
	seedFacilities(t, db)

	ids := resolvedIDs(t, db, ObjectSet{
		Kind:      ObjectSetStatic,
		ObjectIDs: []string{ontologyNodeID("Airport", "LHR")},
	})
	if len(ids) != 1 || ids[0] != ontologyNodeID("Airport", "LHR") {
		t.Fatalf("expected exactly the static ID, got %v", ids)
	}
}

func TestResolveObjectSetFilterOnScalarProperty(t *testing.T) {
	db := openOntologyTestDB(t)
	seedFacilities(t, db)

	source := ObjectSetPredicateSource(ObjectSet{Kind: ObjectSetBase, ObjectType: "Airport"})
	ids := resolvedIDs(t, db, ObjectSet{
		Kind:   ObjectSetFilter,
		Source: &source,
		Where:  &ObjectSetPredicate{Op: PredicateGt, Property: "capacity", Value: "50000"},
	})
	if len(ids) != 1 || ids[0] != ontologyNodeID("Airport", "LHR") {
		t.Fatalf("expected only Heathrow, got %v", ids)
	}
}

func TestResolveObjectSetFilterEqAndIn(t *testing.T) {
	db := openOntologyTestDB(t)
	seedFacilities(t, db)

	source := ObjectSetPredicateSource(ObjectSet{Kind: ObjectSetBase, ObjectType: "Airport"})
	eq := resolvedIDs(t, db, ObjectSet{
		Kind: ObjectSetFilter, Source: &source,
		Where: &ObjectSetPredicate{Op: PredicateEq, Property: "iataCode", Value: "LGW"},
	})
	if len(eq) != 1 {
		t.Fatalf("eq: expected 1, got %v", eq)
	}

	in := resolvedIDs(t, db, ObjectSet{
		Kind: ObjectSetFilter, Source: &source,
		Where: &ObjectSetPredicate{Op: PredicateIn, Property: "iataCode", Values: []string{"LHR", "LGW"}},
	})
	if len(in) != 2 {
		t.Fatalf("in: expected 2, got %v", in)
	}
}

func TestResolveObjectSetBooleanPredicates(t *testing.T) {
	db := openOntologyTestDB(t)
	seedFacilities(t, db)

	source := ObjectSetPredicateSource(ObjectSet{Kind: ObjectSetBase, ObjectType: "Airport"})
	ids := resolvedIDs(t, db, ObjectSet{
		Kind: ObjectSetFilter, Source: &source,
		Where: &ObjectSetPredicate{
			Op: PredicateAnd,
			Operands: []ObjectSetPredicate{
				{Op: PredicateGt, Property: "capacity", Value: "10000"},
				{Op: PredicateNot, Operands: []ObjectSetPredicate{
					{Op: PredicateEq, Property: "iataCode", Value: "LGW"},
				}},
			},
		},
	})
	if len(ids) != 1 || ids[0] != ontologyNodeID("Airport", "LHR") {
		t.Fatalf("expected only Heathrow, got %v", ids)
	}
}

func TestResolveObjectSetUnionIntersectSubtract(t *testing.T) {
	db := openOntologyTestDB(t)
	seedFacilities(t, db)

	airports := ObjectSet{Kind: ObjectSetBase, ObjectType: "Airport"}
	plants := ObjectSet{Kind: ObjectSetBase, ObjectType: "Plant"}
	facilities := ObjectSet{Kind: ObjectSetInterfaceBase, InterfaceType: "Facility"}

	if ids := resolvedIDs(t, db, ObjectSet{Kind: ObjectSetUnion, Operands: []ObjectSet{airports, plants}}); len(ids) != 3 {
		t.Fatalf("union: expected 3, got %v", ids)
	}
	if ids := resolvedIDs(t, db, ObjectSet{Kind: ObjectSetIntersect, Operands: []ObjectSet{facilities, airports}}); len(ids) != 2 {
		t.Fatalf("intersect: expected 2, got %v", ids)
	}
	if ids := resolvedIDs(t, db, ObjectSet{Kind: ObjectSetSubtract, Operands: []ObjectSet{facilities, airports}}); len(ids) != 1 {
		t.Fatalf("subtract: expected 1, got %v", ids)
	}
}

func TestResolveObjectSetRejectsInvalidDefinition(t *testing.T) {
	db := openOntologyTestDB(t)
	seedFacilities(t, db)

	if _, err := db.ResolveObjectSet(context.Background(), ObjectSet{Kind: ObjectSetBase}); err == nil {
		t.Fatal("expected an invalid object set to be rejected before evaluation")
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./pkg/cortexdb -run TestResolveObjectSet -v`
Expected: `undefined: (*DB).ResolveObjectSet`。

- [ ] **Step 3: 写实现**

```go
package cortexdb

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

// objectSetResult is the working representation of an object set: the node
// IDs it contains. Keeping it as a set makes union/intersect/subtract cheap
// and makes repeated evaluation idempotent.
type objectSetResult map[string]struct{}

// ResolveObjectSet evaluates an object set definition to the node IDs it
// selects.
func (db *DB) ResolveObjectSet(ctx context.Context, set ObjectSet) (map[string]struct{}, error) {
	if err := validateObjectSet(set, 0); err != nil {
		return nil, err
	}
	compiled, err := db.activeCompiledOntology(ctx)
	if err != nil {
		return nil, err
	}
	return db.resolveObjectSet(ctx, compiled, set)
}

func (db *DB) resolveObjectSet(ctx context.Context, compiled *compiledOntology, set ObjectSet) (objectSetResult, error) {
	switch set.Kind {
	case ObjectSetBase:
		return db.resolveObjectSetByTypes(ctx, []string{set.ObjectType})

	case ObjectSetInterfaceBase:
		if compiled == nil {
			return nil, fmt.Errorf("interface_base object sets need an active ontology")
		}
		implementors := compiled.implementingObjectTypes(set.InterfaceType)
		if len(implementors) == 0 {
			return objectSetResult{}, nil
		}
		return db.resolveObjectSetByTypes(ctx, implementors)

	case ObjectSetStatic:
		result := make(objectSetResult, len(set.ObjectIDs))
		for _, id := range set.ObjectIDs {
			result[id] = struct{}{}
		}
		return result, nil

	case ObjectSetReference:
		if compiled == nil {
			return nil, fmt.Errorf("reference object sets need an active ontology")
		}
		for _, named := range compiled.schema.ObjectSets {
			if ontologyAPIKey(named.APIName) == ontologyAPIKey(set.Reference) {
				return db.resolveObjectSet(ctx, compiled, named.Definition)
			}
		}
		return nil, fmt.Errorf("ontology defines no saved object set %q", set.Reference)

	case ObjectSetFilter:
		source, err := db.resolveObjectSet(ctx, compiled, ObjectSet(*set.Source))
		if err != nil {
			return nil, err
		}
		return db.applyObjectSetPredicate(ctx, compiled, source, *set.Where)

	case ObjectSetSearchAround:
		source, err := db.resolveObjectSet(ctx, compiled, ObjectSet(*set.Source))
		if err != nil {
			return nil, err
		}
		return db.searchAround(ctx, compiled, source, set.Link)

	case ObjectSetUnion, ObjectSetIntersect, ObjectSetSubtract:
		operands := make([]objectSetResult, 0, len(set.Operands))
		for _, operand := range set.Operands {
			resolved, err := db.resolveObjectSet(ctx, compiled, operand)
			if err != nil {
				return nil, err
			}
			operands = append(operands, resolved)
		}
		return combineObjectSets(set.Kind, operands), nil

	default:
		return nil, fmt.Errorf("unknown object set kind %q", set.Kind)
	}
}

func combineObjectSets(kind ObjectSetKind, operands []objectSetResult) objectSetResult {
	result := make(objectSetResult, len(operands[0]))
	for id := range operands[0] {
		result[id] = struct{}{}
	}

	for _, operand := range operands[1:] {
		switch kind {
		case ObjectSetUnion:
			for id := range operand {
				result[id] = struct{}{}
			}
		case ObjectSetIntersect:
			for id := range result {
				if _, ok := operand[id]; !ok {
					delete(result, id)
				}
			}
		case ObjectSetSubtract:
			for id := range operand {
				delete(result, id)
			}
		}
	}
	return result
}

func (db *DB) resolveObjectSetByTypes(ctx context.Context, objectTypes []string) (objectSetResult, error) {
	if len(objectTypes) == 0 {
		return objectSetResult{}, nil
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(objectTypes)), ",")
	args := make([]any, 0, len(objectTypes))
	for _, objectType := range objectTypes {
		args = append(args, objectType)
	}

	rows, err := db.store.GetDB().QueryContext(ctx,
		`SELECT id FROM graph_nodes WHERE node_type IN (`+placeholders+`)`, args...)
	if err != nil {
		return nil, fmt.Errorf("resolve object set by type: %w", err)
	}
	defer func() { _ = rows.Close() }()

	result := objectSetResult{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan object set row: %w", err)
		}
		result[id] = struct{}{}
	}
	return result, rows.Err()
}

// applyObjectSetPredicate narrows a source set. Boolean operators recurse;
// leaf predicates read the property off each node.
func (db *DB) applyObjectSetPredicate(ctx context.Context, compiled *compiledOntology, source objectSetResult, predicate ObjectSetPredicate) (objectSetResult, error) {
	switch predicate.Op {
	case PredicateAnd:
		result := source
		for _, operand := range predicate.Operands {
			narrowed, err := db.applyObjectSetPredicate(ctx, compiled, result, operand)
			if err != nil {
				return nil, err
			}
			result = narrowed
		}
		return result, nil

	case PredicateOr:
		union := objectSetResult{}
		for _, operand := range predicate.Operands {
			matched, err := db.applyObjectSetPredicate(ctx, compiled, source, operand)
			if err != nil {
				return nil, err
			}
			for id := range matched {
				union[id] = struct{}{}
			}
		}
		return union, nil

	case PredicateNot:
		excluded, err := db.applyObjectSetPredicate(ctx, compiled, source, predicate.Operands[0])
		if err != nil {
			return nil, err
		}
		result := objectSetResult{}
		for id := range source {
			if _, ok := excluded[id]; !ok {
				result[id] = struct{}{}
			}
		}
		return result, nil

	case PredicateContainsAllTerms, PredicateContainsAnyTerm:
		return db.applyObjectSetTextPredicate(ctx, source, predicate)

	case PredicateNearestNeighbors:
		return db.applyObjectSetVectorPredicate(ctx, source, predicate)

	default:
		return db.applyObjectSetScalarPredicate(ctx, compiled, source, predicate)
	}
}

func (db *DB) applyObjectSetScalarPredicate(ctx context.Context, compiled *compiledOntology, source objectSetResult, predicate ObjectSetPredicate) (objectSetResult, error) {
	values, err := db.loadNodePropertyValues(ctx, source, predicate.Property)
	if err != nil {
		return nil, err
	}

	result := objectSetResult{}
	for id := range source {
		value, present := values[id]
		matched, err := matchScalarPredicate(predicate, value, present)
		if err != nil {
			return nil, err
		}
		if matched {
			result[id] = struct{}{}
		}
	}
	return result, nil
}

func matchScalarPredicate(predicate ObjectSetPredicate, value string, present bool) (bool, error) {
	if predicate.Op == PredicateIsNull {
		return !present || strings.TrimSpace(value) == "", nil
	}
	if !present {
		return false, nil
	}

	switch predicate.Op {
	case PredicateEq:
		return value == predicate.Value, nil
	case PredicateContains:
		return strings.Contains(strings.ToLower(value), strings.ToLower(predicate.Value)), nil
	case PredicateStartsWith:
		return strings.HasPrefix(strings.ToLower(value), strings.ToLower(predicate.Value)), nil
	case PredicateIn:
		for _, candidate := range predicate.Values {
			if value == candidate {
				return true, nil
			}
		}
		return false, nil
	case PredicateLt, PredicateLte, PredicateGt, PredicateGte:
		return compareOrderedPredicate(predicate.Op, value, predicate.Value)
	default:
		return false, fmt.Errorf("predicate %q is not a scalar predicate", predicate.Op)
	}
}

// compareOrderedPredicate compares numerically when both sides parse as
// numbers and lexicographically otherwise, so dates and codes still order
// sensibly.
func compareOrderedPredicate(op PredicateOp, left string, right string) (bool, error) {
	leftNum, leftErr := strconv.ParseFloat(left, 64)
	rightNum, rightErr := strconv.ParseFloat(right, 64)

	var cmp int
	if leftErr == nil && rightErr == nil {
		switch {
		case leftNum < rightNum:
			cmp = -1
		case leftNum > rightNum:
			cmp = 1
		}
	} else {
		cmp = strings.Compare(left, right)
	}

	switch op {
	case PredicateLt:
		return cmp < 0, nil
	case PredicateLte:
		return cmp <= 0, nil
	case PredicateGt:
		return cmp > 0, nil
	case PredicateGte:
		return cmp >= 0, nil
	default:
		return false, fmt.Errorf("predicate %q is not an ordered comparison", op)
	}
}
```

`loadNodePropertyValues` 从 `graph_nodes.properties`（JSON 列）里取属性。先跑 `grep -n "properties" pkg/graph/graph.go | head -20` 确认列名与编码，再实现：

```go
func (db *DB) loadNodePropertyValues(ctx context.Context, source objectSetResult, property string) (map[string]string, error) {
	if len(source) == 0 {
		return map[string]string{}, nil
	}
	nodeIDs := make([]string, 0, len(source))
	for id := range source {
		nodeIDs = append(nodeIDs, id)
	}
	nodes, err := db.graph.GetNodesBatch(ctx, nodeIDs)
	if err != nil {
		return nil, fmt.Errorf("load node properties: %w", err)
	}

	key := ontologyAPIKey(property)
	values := make(map[string]string, len(nodes))
	for _, node := range nodes {
		if node == nil {
			continue
		}
		for name, raw := range node.Properties {
			if ontologyAPIKey(name) != key {
				continue
			}
			if text, ok := raw.(string); ok {
				values[node.ID] = text
			} else {
				values[node.ID] = fmt.Sprintf("%v", raw)
			}
			break
		}
	}
	return values, nil
}
```

本任务里 `applyObjectSetTextPredicate`、`applyObjectSetVectorPredicate`、`searchAround` 先写成返回 `fmt.Errorf("not implemented")` 的桩，Task 14/15 填实现。

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./pkg/cortexdb -run TestResolveObjectSet -v`
Expected: PASS ×8。

- [ ] **Step 5: Commit**

```bash
git add pkg/cortexdb/objectset_resolve.go pkg/cortexdb/objectset_resolve_test.go
git commit -m "feat(objectset): resolve base, static, set operations and scalar predicates"
```

---

### Task 14: searchAround —— 沿链接遍历

**Files:**
- Modify: `pkg/cortexdb/objectset_resolve.go`（替换 `searchAround` 桩）
- Test: `pkg/cortexdb/objectset_searcharound_test.go`

- [ ] **Step 1: 写失败的测试**

```go
package cortexdb

import (
	"context"
	"sort"
	"testing"
)

func seedAviationGraph(t *testing.T, db *DB) {
	t.Helper()
	ctx := context.Background()
	activateAviationSchema(t, db)

	tools := db.GraphRAGTools()
	if _, err := tools.UpsertEntities(ctx, ToolUpsertEntitiesRequest{Entities: []ToolEntityInput{
		{Name: "London Heathrow", Type: "Airport", Metadata: map[string]string{"iataCode": "LHR"}},
		{Name: "Gatwick", Type: "Airport", Metadata: map[string]string{"iataCode": "LGW"}},
		{Name: "BA117", Type: "Flight", Metadata: map[string]string{"flightNumber": "BA117"}},
		{Name: "BA118", Type: "Flight", Metadata: map[string]string{"flightNumber": "BA118"}},
		{Name: "U28422", Type: "Flight", Metadata: map[string]string{"flightNumber": "U28422"}},
	}}); err != nil {
		t.Fatalf("seed entities: %v", err)
	}
	if _, err := tools.UpsertRelations(ctx, ToolUpsertRelationsRequest{Relations: []ToolRelationInput{
		{From: "London Heathrow", To: "BA117", Type: "flightDeparture"},
		{From: "London Heathrow", To: "BA118", Type: "flightDeparture"},
		{From: "Gatwick", To: "U28422", Type: "flightDeparture"},
	}}); err != nil {
		t.Fatalf("seed relations: %v", err)
	}
}

func TestSearchAroundFollowsTheNamedSide(t *testing.T) {
	db := openOntologyTestDB(t)
	seedAviationGraph(t, db)

	// From Heathrow, traverse the "departures" side to reach its flights.
	source := ObjectSetPredicateSource(ObjectSet{
		Kind: ObjectSetStatic, ObjectIDs: []string{ontologyNodeID("Airport", "LHR")},
	})
	ids := resolvedIDs(t, db, ObjectSet{Kind: ObjectSetSearchAround, Source: &source, Link: "departures"})

	sort.Strings(ids)
	if len(ids) != 2 {
		t.Fatalf("expected Heathrow's 2 flights, got %v", ids)
	}
	if ids[0] != ontologyNodeID("Flight", "BA117") {
		t.Fatalf("unexpected result: %v", ids)
	}
}

func TestSearchAroundTraversesTheOtherDirection(t *testing.T) {
	db := openOntologyTestDB(t)
	seedAviationGraph(t, db)

	// From all flights, traverse "origin" back to their airports.
	source := ObjectSetPredicateSource(ObjectSet{Kind: ObjectSetBase, ObjectType: "Flight"})
	ids := resolvedIDs(t, db, ObjectSet{Kind: ObjectSetSearchAround, Source: &source, Link: "origin"})

	if len(ids) != 2 {
		t.Fatalf("expected both airports, got %v", ids)
	}
}

func TestSearchAroundRejectsUnknownLinkSide(t *testing.T) {
	db := openOntologyTestDB(t)
	seedAviationGraph(t, db)

	source := ObjectSetPredicateSource(ObjectSet{Kind: ObjectSetBase, ObjectType: "Flight"})
	_, err := db.ResolveObjectSet(context.Background(), ObjectSet{
		Kind: ObjectSetSearchAround, Source: &source, Link: "refuelsAt",
	})
	if err == nil {
		t.Fatal("expected an unknown link side to be rejected")
	}
}

func TestSearchAroundChainsTwice(t *testing.T) {
	db := openOntologyTestDB(t)
	seedAviationGraph(t, db)

	// Heathrow -> its flights -> back to their origin airports (Heathrow).
	base := ObjectSetPredicateSource(ObjectSet{
		Kind: ObjectSetStatic, ObjectIDs: []string{ontologyNodeID("Airport", "LHR")},
	})
	firstHop := ObjectSetPredicateSource(ObjectSet{Kind: ObjectSetSearchAround, Source: &base, Link: "departures"})
	ids := resolvedIDs(t, db, ObjectSet{Kind: ObjectSetSearchAround, Source: &firstHop, Link: "origin"})

	if len(ids) != 1 || ids[0] != ontologyNodeID("Airport", "LHR") {
		t.Fatalf("expected to come back to Heathrow, got %v", ids)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./pkg/cortexdb -run TestSearchAround -v`
Expected: `not implemented`。

- [ ] **Step 3: 写实现**

先在 `ontology_compile.go` 加一个按侧名反查链接类型的方法：

```go
// linkTypeBySide finds the link type owning a traversal side name, and
// returns the side itself plus the side on the far end.
func (c *compiledOntology) linkTypeBySide(sideAPIName string) (OntologyLinkType, OntologyLinkSide, OntologyLinkSide, bool) {
	key := ontologyAPIKey(sideAPIName)
	for _, linkType := range c.linkTypes {
		if ontologyAPIKey(linkType.A.APIName) == key {
			return linkType, linkType.A, linkType.B, true
		}
		if ontologyAPIKey(linkType.B.APIName) == key {
			return linkType, linkType.B, linkType.A, true
		}
	}
	return OntologyLinkType{}, OntologyLinkSide{}, OntologyLinkSide{}, false
}
```

然后替换 `objectset_resolve.go` 里的 `searchAround` 桩：

```go
// searchAround traverses one link side from every object in the source set.
// The side name fixes both which link type to follow and which direction, so
// "departures" and "origin" walk the same edges opposite ways.
func (db *DB) searchAround(ctx context.Context, compiled *compiledOntology, source objectSetResult, sideAPIName string) (objectSetResult, error) {
	if compiled == nil {
		return nil, fmt.Errorf("search_around object sets need an active ontology")
	}
	linkType, nearSide, farSide, ok := compiled.linkTypeBySide(sideAPIName)
	if !ok {
		return nil, fmt.Errorf("ontology defines no link side %q", sideAPIName)
	}
	if len(source) == 0 {
		return objectSetResult{}, nil
	}

	// Traversing the side named `nearSide` means: start from objects of the
	// *far* side's object type... no — start from objects that own this side,
	// which are of farSide's object type, and land on nearSide's type.
	// Concretely: Airport owns "departures", which lands on Flight.
	_ = nearSide

	nodeIDs := make([]string, 0, len(source))
	for id := range source {
		nodeIDs = append(nodeIDs, id)
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(nodeIDs)), ",")

	args := make([]any, 0, len(nodeIDs)*2+2)
	args = append(args, linkType.APIName)
	for _, id := range nodeIDs {
		args = append(args, id)
	}
	for _, id := range nodeIDs {
		args = append(args, id)
	}

	rows, err := db.store.GetDB().QueryContext(ctx, `
		SELECT to_node_id AS other FROM graph_edges
		WHERE edge_type = ? AND from_node_id IN (`+placeholders+`)
		UNION
		SELECT from_node_id AS other FROM graph_edges
		WHERE edge_type = ? AND to_node_id IN (`+placeholders+`)
	`, append([]any{linkType.APIName}, args[1:]...)...)
	if err != nil {
		return nil, fmt.Errorf("search around: %w", err)
	}
	defer func() { _ = rows.Close() }()

	reached := make([]string, 0)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan search around row: %w", err)
		}
		reached = append(reached, id)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// A link type is bidirectional, so the SQL above reaches both ends.
	// Keeping only nodes of the target side's object type is what makes the
	// side name — not just the link type — decide the direction.
	return db.filterByObjectType(ctx, reached, farSide.ObjectTypeAPIName)
}

func (db *DB) filterByObjectType(ctx context.Context, nodeIDs []string, objectTypeAPIName string) (objectSetResult, error) {
	result := objectSetResult{}
	if len(nodeIDs) == 0 {
		return result, nil
	}
	nodes, err := db.graph.GetNodesBatch(ctx, nodeIDs)
	if err != nil {
		return nil, fmt.Errorf("filter search around results: %w", err)
	}
	target := ontologyAPIKey(objectTypeAPIName)
	for _, node := range nodes {
		if node != nil && ontologyAPIKey(node.NodeType) == target {
			result[node.ID] = struct{}{}
		}
	}
	return result, nil
}
```

> 实现者注意上面 SQL 参数拼装那段：写成 `args` 后又用 `append([]any{...}, args[1:]...)` 是重复的。落地时清理成一次构造，参数顺序为 `[linkType, nodeIDs..., linkType, nodeIDs...]`，与两段 UNION 对应。测试会抓出参数错位。

另外注意 `farSide` 的语义：`departures` 这个侧名挂在 `Airport` 上（`A.APIName == "departures"`, `A.ObjectTypeAPIName == "Airport"`），而从 Heathrow 出发沿 `departures` 应到达 `Flight`——也就是 `B.ObjectTypeAPIName`。`linkTypeBySide` 返回的 `farSide` 正是这个。若测试显示方向反了，交换 `linkTypeBySide` 里 `nearSide`/`farSide` 的返回顺序即可，并删掉上面那句 `_ = nearSide`。

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./pkg/cortexdb -run TestSearchAround -v`
Expected: PASS ×4。

- [ ] **Step 5: Commit**

```bash
git add pkg/cortexdb/objectset_resolve.go pkg/cortexdb/ontology_compile.go \
        pkg/cortexdb/objectset_searcharound_test.go
git commit -m "feat(objectset): traverse link sides with search_around"
```

---

### Task 15: 文本与向量谓词 + 工具面

**Files:**
- Modify: `pkg/cortexdb/objectset_resolve.go`（替换文本/向量两个桩）
- Modify: `pkg/cortexdb/ontology_api.go`（加 `ObjectSetResolveRequest/Response`）
- Modify: `pkg/cortexdb/ontology_tooldefs.go`、`ontology_dispatch.go`、`ontology_mcp.go`
- Test: `pkg/cortexdb/objectset_hybrid_test.go`

- [ ] **Step 1: 写失败的测试**

```go
package cortexdb

import (
	"context"
	"testing"
)

func TestObjectSetTextPredicateUsesSearchableProperties(t *testing.T) {
	db := openOntologyTestDB(t)
	seedFacilities(t, db)

	source := ObjectSetPredicateSource(ObjectSet{Kind: ObjectSetInterfaceBase, InterfaceType: "Facility"})
	ids := resolvedIDs(t, db, ObjectSet{
		Kind: ObjectSetFilter, Source: &source,
		Where: &ObjectSetPredicate{Op: PredicateContainsAllTerms, Property: "facilityName", Value: "london heathrow"},
	})
	if len(ids) != 1 || ids[0] != ontologyNodeID("Airport", "LHR") {
		t.Fatalf("expected only Heathrow, got %v", ids)
	}
}

func TestObjectSetContainsAnyTerm(t *testing.T) {
	db := openOntologyTestDB(t)
	seedFacilities(t, db)

	source := ObjectSetPredicateSource(ObjectSet{Kind: ObjectSetInterfaceBase, InterfaceType: "Facility"})
	ids := resolvedIDs(t, db, ObjectSet{
		Kind: ObjectSetFilter, Source: &source,
		Where: &ObjectSetPredicate{Op: PredicateContainsAnyTerm, Property: "facilityName", Value: "gatwick sunderland"},
	})
	if len(ids) != 2 {
		t.Fatalf("expected Gatwick and Sunderland, got %v", ids)
	}
}

func TestObjectSetVectorPredicateRequiresEmbedder(t *testing.T) {
	db := openOntologyTestDB(t)
	seedFacilities(t, db)

	source := ObjectSetPredicateSource(ObjectSet{Kind: ObjectSetBase, ObjectType: "Airport"})
	_, err := db.ResolveObjectSet(context.Background(), ObjectSet{
		Kind: ObjectSetFilter, Source: &source,
		Where: &ObjectSetPredicate{Op: PredicateNearestNeighbors, Property: "embedding", Value: "busy london hub", K: 1},
	})
	// No embedder is configured in this test DB, so the resolver must say so
	// plainly rather than silently returning everything or nothing.
	if err == nil {
		t.Fatal("expected a clear error when a vector predicate runs without an embedder")
	}
}

func TestResolveObjectSetToolIsReachable(t *testing.T) {
	db := openOntologyTestDB(t)
	seedFacilities(t, db)

	resp, err := db.GraphRAGTools().ResolveObjectSet(context.Background(), ObjectSetResolveRequest{
		ObjectSet: ObjectSet{Kind: ObjectSetInterfaceBase, InterfaceType: "Facility"},
		Limit:     10,
	})
	if err != nil {
		t.Fatalf("resolve tool: %v", err)
	}
	if len(resp.Objects) != 3 {
		t.Fatalf("expected 3 facilities, got %d", len(resp.Objects))
	}
	if resp.Objects[0].ObjectType == "" {
		t.Fatal("resolved objects must carry their object type")
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./pkg/cortexdb -run 'TestObjectSet|TestResolveObjectSetTool' -v`
Expected: 文本谓词 `not implemented`；`undefined: ObjectSetResolveRequest`。

- [ ] **Step 3: 写实现**

文本谓词——`Searchable` 属性已随实体写入 FTS5，这里对候选集做词项匹配。最简实现是在内存里对属性值做词项判定，避免和 FTS5 的 tokenizer 细节耦合：

```go
func (db *DB) applyObjectSetTextPredicate(ctx context.Context, source objectSetResult, predicate ObjectSetPredicate) (objectSetResult, error) {
	values, err := db.loadNodePropertyValues(ctx, source, predicate.Property)
	if err != nil {
		return nil, err
	}
	terms := strings.Fields(strings.ToLower(predicate.Value))
	if len(terms) == 0 {
		return objectSetResult{}, nil
	}

	result := objectSetResult{}
	for id := range source {
		haystack := strings.ToLower(values[id])
		matched := predicate.Op == PredicateContainsAllTerms
		for _, term := range terms {
			contains := strings.Contains(haystack, term)
			if predicate.Op == PredicateContainsAllTerms && !contains {
				matched = false
				break
			}
			if predicate.Op == PredicateContainsAnyTerm && contains {
				matched = true
				break
			}
		}
		if matched {
			result[id] = struct{}{}
		}
	}
	return result, nil
}
```

向量谓词——复用已有的向量检索路径，把 KNN 结果与候选集求交：

```go
func (db *DB) applyObjectSetVectorPredicate(ctx context.Context, source objectSetResult, predicate ObjectSetPredicate) (objectSetResult, error) {
	queryVector := predicate.Vector
	if len(queryVector) == 0 {
		if db.embedder == nil {
			return nil, fmt.Errorf("predicate %q needs either an explicit vector or a configured embedder", predicate.Op)
		}
		embedded, err := db.embedder.Embed(ctx, predicate.Value)
		if err != nil {
			return nil, fmt.Errorf("embed nearest_neighbors query: %w", err)
		}
		queryVector = embedded
	}

	k := predicate.K
	if k <= 0 {
		k = 10
	}
	neighbours, err := db.graph.SearchNodesByVector(ctx, queryVector, k)
	if err != nil {
		return nil, fmt.Errorf("nearest neighbours: %w", err)
	}

	result := objectSetResult{}
	for _, neighbour := range neighbours {
		if _, ok := source[neighbour.ID]; ok {
			result[neighbour.ID] = struct{}{}
		}
	}
	return result, nil
}
```

先跑 `grep -n "func (g \*GraphStore)" pkg/graph/graph_hnsw.go pkg/graph/graph_hybrid.go | grep -i vector` 确认实际的向量检索方法名与签名，并据此改上面的调用；同样确认 `db.embedder` 字段名（`grep -n "embedder" pkg/cortexdb/cortexdb.go`）。

工具面——在 `ontology_api.go` 追加：

```go
// ObjectSetResolveRequest evaluates an object set and returns its members.
type ObjectSetResolveRequest struct {
	ObjectSet ObjectSet `json:"object_set"`
	Limit     int       `json:"limit,omitempty"`
}

// ResolvedObject is one member of a resolved object set.
type ResolvedObject struct {
	ObjectID   string            `json:"object_id"`
	ObjectType string            `json:"object_type"`
	Title      string            `json:"title,omitempty"`
	Properties map[string]string `json:"properties,omitempty"`
}

// ObjectSetResolveResponse returns the resolved members.
type ObjectSetResolveResponse struct {
	Objects []ResolvedObject `json:"objects"`
	Total   int              `json:"total"`
}

func (db *DB) ResolveObjectSetObjects(ctx context.Context, req ObjectSetResolveRequest) (*ObjectSetResolveResponse, error) {
	resolved, err := db.ResolveObjectSet(ctx, req.ObjectSet)
	if err != nil {
		return nil, err
	}

	nodeIDs := make([]string, 0, len(resolved))
	for id := range resolved {
		nodeIDs = append(nodeIDs, id)
	}
	sort.Strings(nodeIDs)

	total := len(nodeIDs)
	if req.Limit > 0 && len(nodeIDs) > req.Limit {
		nodeIDs = nodeIDs[:req.Limit]
	}

	nodes, err := db.graph.GetNodesBatch(ctx, nodeIDs)
	if err != nil {
		return nil, fmt.Errorf("load resolved objects: %w", err)
	}

	objects := make([]ResolvedObject, 0, len(nodes))
	for _, node := range nodes {
		if node == nil {
			continue
		}
		properties := make(map[string]string, len(node.Properties))
		for name, raw := range node.Properties {
			properties[name] = fmt.Sprintf("%v", raw)
		}
		objects = append(objects, ResolvedObject{
			ObjectID:   node.ID,
			ObjectType: node.NodeType,
			Title:      node.Content,
			Properties: properties,
		})
	}
	return &ObjectSetResolveResponse{Objects: objects, Total: total}, nil
}

func (t *GraphRAGToolbox) ResolveObjectSet(ctx context.Context, req ObjectSetResolveRequest) (*ObjectSetResolveResponse, error) {
	return t.db.ResolveObjectSetObjects(ctx, req)
}
```

`ontology_api.go` 顶部 import 加 `"fmt"`、`"sort"`。

工具定义追加到 `ontologyToolDefinitions()`：

```go
{
	Name:        "object_set_resolve",
	Description: "Evaluate an object set and return its members. An object set composes base/interface_base/static sources with filter, search_around (traverse a link side), union, intersect and subtract. Filters support eq/lt/lte/gt/gte/in/is_null/contains/starts_with/contains_all_terms/contains_any_term/nearest_neighbors plus and/or/not. At most 3 chained search_around hops.",
	InputSchema: toolObjectSchema(
		[]string{"object_set"},
		map[string]any{
			"object_set": map[string]any{
				"type":        "object",
				"description": "Object set definition. Requires kind: base|interface_base|static|reference|filter|search_around|union|intersect|subtract. base needs object_type; interface_base needs interface_type; static needs object_ids; filter needs source and where; search_around needs source and link (a link side api name); set operations need operands (>=2).",
			},
			"limit": toolIntegerSchema("Maximum objects to return. Total is reported separately."),
		},
	),
},
```

dispatch 分支加到 `callOntologyTool`：

```go
case "object_set_resolve":
	var req ObjectSetResolveRequest
	if err := json.Unmarshal(input, &req); err != nil {
		return nil, true, fmt.Errorf("decode %s: %w", name, err)
	}
	resp, err := t.ResolveObjectSet(ctx, req)
	return resp, true, err
```

MCP 注册加到 `addOntologyMCPTools`：

```go
addGraphRAGMCPTool(server, definitions["object_set_resolve"], func(ctx context.Context, req ObjectSetResolveRequest) (ObjectSetResolveResponse, error) {
	resp, err := toolbox.ResolveObjectSet(ctx, req)
	if err != nil {
		return ObjectSetResolveResponse{}, err
	}
	if resp == nil {
		return ObjectSetResolveResponse{}, nil
	}
	return *resp, nil
})
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test -race ./pkg/cortexdb`
Expected: 全部 PASS，含 `TestEveryToolDefinitionIsReachableOverMCP`（新工具必须同时出现在定义和 MCP 注册两处，漏一处就红）。

- [ ] **Step 5: Commit 第 4 期**

```bash
git add pkg/cortexdb/objectset_resolve.go pkg/cortexdb/ontology_api.go \
        pkg/cortexdb/ontology_tooldefs.go pkg/cortexdb/ontology_dispatch.go \
        pkg/cortexdb/ontology_mcp.go pkg/cortexdb/objectset_hybrid_test.go
git commit -m "feat(objectset): add text and vector predicates, expose object_set_resolve

Vector KNN, full-text terms and link traversal are now peer operators in one
composable expression, rather than three separate retrieval APIs."
```

---

# 第 5 期：Action Types

Palantir 的核心主张：**治理之下的写入**。与其让 agent 自由 upsert，不如只暴露白名单 action。本期照抄 Foundry 的三条硬约束：

1. `create_link` 只处理多对多；一对多/一对一必须走 `modify_object` 改外键属性
2. `modify_object` / `delete_object` 不能引用同一个 action 里刚创建的对象
3. Validate 只校验参数与提交条件，**不查已有数据**（不检查主键唯一性）

### Task 16: Action 类型定义与 schema 校验

**Files:**
- Modify: `pkg/cortexdb/ontology_action_types.go`（替换第 1 期占位）
- Modify: `pkg/cortexdb/ontology_validation.go`（加 action 校验）
- Test: `pkg/cortexdb/ontology_action_types_test.go`

- [ ] **Step 1: 写失败的测试**

```go
package cortexdb

import (
	"strings"
	"testing"
)

func aviationSchemaWithActions() OntologySchema {
	schema := validAviationSchema()
	schema.ActionTypes = []OntologyActionType{
		{
			APIName:     "registerAirport",
			DisplayName: "Register Airport",
			Parameters: []OntologyActionParameter{
				{APIName: "iataCode", DataType: OntologyDataType{Kind: OntologyDataString}, Required: true},
				{APIName: "airportName", DataType: OntologyDataType{Kind: OntologyDataString}, Required: true},
			},
			Rules: []OntologyActionRule{
				{
					Kind:       ActionRuleCreateObject,
					ObjectType: "Airport",
					PropertyValues: map[string]OntologyValueSource{
						"iataCode":    {Kind: ValueSourceParameter, Parameter: "iataCode"},
						"airportName": {Kind: ValueSourceParameter, Parameter: "airportName"},
					},
				},
			},
			SubmissionCriteria: []OntologySubmissionCriterion{
				{
					Parameter:      "iataCode",
					Op:             PredicateContains,
					Value:          "",
					Regex:          "^[A-Z]{3}$",
					FailureMessage: "IATA code must be three uppercase letters.",
				},
			},
		},
	}
	return schema
}

func TestValidateSchemaAcceptsActionTypes(t *testing.T) {
	if err := validateOntologySchema(aviationSchemaWithActions()); err != nil {
		t.Fatalf("expected valid action types, got %v", err)
	}
}

func TestValidateSchemaRejectsActionRuleOnUnknownObjectType(t *testing.T) {
	schema := aviationSchemaWithActions()
	schema.ActionTypes[0].Rules[0].ObjectType = "Spacecraft"

	err := validateOntologySchema(schema)
	if err == nil || !strings.Contains(err.Error(), "Spacecraft") {
		t.Fatalf("expected unknown object type to be rejected, got %v", err)
	}
}

func TestValidateSchemaRejectsActionRuleOnUnknownProperty(t *testing.T) {
	schema := aviationSchemaWithActions()
	schema.ActionTypes[0].Rules[0].PropertyValues["runwayCount"] = OntologyValueSource{
		Kind: ValueSourceStatic, Static: "2",
	}

	err := validateOntologySchema(schema)
	if err == nil || !strings.Contains(err.Error(), "runwayCount") {
		t.Fatalf("expected unknown property to be rejected, got %v", err)
	}
}

func TestValidateSchemaRejectsValueSourceOnUnknownParameter(t *testing.T) {
	schema := aviationSchemaWithActions()
	schema.ActionTypes[0].Rules[0].PropertyValues["iataCode"] = OntologyValueSource{
		Kind: ValueSourceParameter, Parameter: "nope",
	}

	err := validateOntologySchema(schema)
	if err == nil || !strings.Contains(err.Error(), "nope") {
		t.Fatalf("expected unknown parameter reference to be rejected, got %v", err)
	}
}

func TestValidateSchemaRequiresCreateObjectRuleToSupplyPrimaryKey(t *testing.T) {
	schema := aviationSchemaWithActions()
	delete(schema.ActionTypes[0].Rules[0].PropertyValues, "iataCode")

	err := validateOntologySchema(schema)
	if err == nil || !strings.Contains(err.Error(), "iataCode") {
		t.Fatalf("expected a create rule without the primary key to be rejected, got %v", err)
	}
}

func TestValidateSchemaRejectsCreateLinkOnNonManyToManyLink(t *testing.T) {
	schema := aviationSchemaWithActions()
	// flightDeparture is one-to-many (origin side is ONE), so Foundry
	// requires the foreign key to be modified instead of a link rule.
	schema.ActionTypes[0].Rules = append(schema.ActionTypes[0].Rules, OntologyActionRule{
		Kind:     ActionRuleCreateLink,
		LinkType: "flightDeparture",
		From:     OntologyValueSource{Kind: ValueSourceParameter, Parameter: "iataCode"},
		To:       OntologyValueSource{Kind: ValueSourceParameter, Parameter: "airportName"},
	})

	err := validateOntologySchema(schema)
	if err == nil || !strings.Contains(err.Error(), "many-to-many") {
		t.Fatalf("expected create_link on a one-to-many link to be rejected, got %v", err)
	}
}

func TestValidateSchemaRejectsDuplicateActionAPINames(t *testing.T) {
	schema := aviationSchemaWithActions()
	schema.ActionTypes = append(schema.ActionTypes, schema.ActionTypes[0])

	if err := validateOntologySchema(schema); err == nil {
		t.Fatal("expected duplicate action api names to be rejected")
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./pkg/cortexdb -run 'TestValidateSchemaAcceptsActionTypes|TestValidateSchemaRejectsAction|TestValidateSchemaRejectsValueSource|TestValidateSchemaRequiresCreateObject|TestValidateSchemaRejectsCreateLink|TestValidateSchemaRejectsDuplicateAction' -v`
Expected: `undefined: OntologyActionParameter`。

- [ ] **Step 3: 写实现**

清空 `pkg/cortexdb/ontology_action_types.go` 并写入：

```go
package cortexdb

import (
	"fmt"
	"strings"
)

// ActionRuleKind enumerates the ontology edits an action can make.
// Foundry also has function rules and side-effect rules (notification,
// webhook, schedule build); those need a runtime CortexDB deliberately does
// not have, so they are out of scope.
type ActionRuleKind string

const (
	ActionRuleCreateObject         ActionRuleKind = "create_object"
	ActionRuleModifyObject         ActionRuleKind = "modify_object"
	ActionRuleCreateOrModifyObject ActionRuleKind = "create_or_modify_object"
	ActionRuleDeleteObject         ActionRuleKind = "delete_object"
	ActionRuleCreateLink           ActionRuleKind = "create_link"
	ActionRuleDeleteLink           ActionRuleKind = "delete_link"
)

// ValueSourceKind is where a rule gets a value from.
type ValueSourceKind string

const (
	ValueSourceParameter       ValueSourceKind = "parameter"
	ValueSourceObjectProperty  ValueSourceKind = "object_property"
	ValueSourceStatic          ValueSourceKind = "static"
	ValueSourceCurrentUser     ValueSourceKind = "current_user"
	ValueSourceCurrentTime     ValueSourceKind = "current_time"
)

// OntologyValueSource resolves to a concrete value at apply time.
type OntologyValueSource struct {
	Kind ValueSourceKind `json:"kind"`
	// parameter
	Parameter string `json:"parameter,omitempty"`
	// object_property: read Property off the object the named parameter points at
	Property string `json:"property,omitempty"`
	// static
	Static string `json:"static,omitempty"`
}

// OntologyActionParameter is one input to an action.
type OntologyActionParameter struct {
	APIName     string           `json:"api_name"`
	DisplayName string           `json:"display_name,omitempty"`
	Description string           `json:"description,omitempty"`
	DataType    OntologyDataType `json:"data_type"`
	Required    bool             `json:"required,omitempty"`
	// ObjectType marks this as an object reference parameter: its value is
	// the node ID of an existing object of that type.
	ObjectType string `json:"object_type,omitempty"`
	// AllowedValues restricts the parameter to a fixed set.
	AllowedValues []string `json:"allowed_values,omitempty"`
}

// OntologyActionRule is one edit an action makes.
type OntologyActionRule struct {
	Kind ActionRuleKind `json:"kind"`
	// object rules
	ObjectType string `json:"object_type,omitempty"`
	// Target names the object reference parameter identifying which object
	// to modify or delete.
	Target         string                         `json:"target,omitempty"`
	PropertyValues map[string]OntologyValueSource `json:"property_values,omitempty"`
	// link rules
	LinkType string              `json:"link_type,omitempty"`
	From     OntologyValueSource `json:"from,omitempty"`
	To       OntologyValueSource `json:"to,omitempty"`
}

// OntologySubmissionCriterion gates whether an action may be submitted.
// Criteria see parameters only, never the graph — matching Foundry's
// Validate Action, which explicitly does not consult existing data.
type OntologySubmissionCriterion struct {
	Parameter      string      `json:"parameter"`
	Op             PredicateOp `json:"op,omitempty"`
	Value          string      `json:"value,omitempty"`
	Values         []string    `json:"values,omitempty"`
	Regex          string      `json:"regex,omitempty"`
	FailureMessage string      `json:"failure_message,omitempty"`
}

// OntologyActionType is a governed, auditable set of graph edits.
type OntologyActionType struct {
	APIName            string                        `json:"api_name"`
	DisplayName        string                        `json:"display_name,omitempty"`
	Description        string                        `json:"description,omitempty"`
	Status             OntologyStatus                `json:"status,omitempty"`
	Parameters         []OntologyActionParameter     `json:"parameters,omitempty"`
	Rules              []OntologyActionRule          `json:"rules,omitempty"`
	SubmissionCriteria []OntologySubmissionCriterion `json:"submission_criteria,omitempty"`
}

func validateOntologyActionTypes(schema OntologySchema, compiled *compiledOntology) error {
	seen := make(map[string]struct{}, len(schema.ActionTypes))
	for _, action := range schema.ActionTypes {
		if err := validateOntologyAPIName("action type", action.APIName); err != nil {
			return err
		}
		key := ontologyAPIKey(action.APIName)
		if _, exists := seen[key]; exists {
			return fmt.Errorf("duplicate action type %q", action.APIName)
		}
		seen[key] = struct{}{}

		parameters := make(map[string]OntologyActionParameter, len(action.Parameters))
		for _, parameter := range action.Parameters {
			if err := validateOntologyAPIName(fmt.Sprintf("action %s parameter", action.APIName), parameter.APIName); err != nil {
				return err
			}
			if parameter.ObjectType != "" {
				if _, ok := compiled.objectType(parameter.ObjectType); !ok {
					return fmt.Errorf("action %q parameter %q references unknown object type %q",
						action.APIName, parameter.APIName, parameter.ObjectType)
				}
			}
			parameters[ontologyAPIKey(parameter.APIName)] = parameter
		}

		if len(action.Rules) == 0 {
			return fmt.Errorf("action type %q must declare at least one rule", action.APIName)
		}
		for _, rule := range action.Rules {
			if err := validateOntologyActionRule(action, rule, parameters, compiled); err != nil {
				return err
			}
		}
		for _, criterion := range action.SubmissionCriteria {
			if _, ok := parameters[ontologyAPIKey(criterion.Parameter)]; !ok {
				return fmt.Errorf("action %q submission criterion references unknown parameter %q",
					action.APIName, criterion.Parameter)
			}
		}
	}
	return nil
}

func validateOntologyActionRule(action OntologyActionType, rule OntologyActionRule, parameters map[string]OntologyActionParameter, compiled *compiledOntology) error {
	checkSource := func(source OntologyValueSource, label string) error {
		switch source.Kind {
		case ValueSourceParameter, ValueSourceObjectProperty:
			if _, ok := parameters[ontologyAPIKey(source.Parameter)]; !ok {
				return fmt.Errorf("action %q %s references unknown parameter %q", action.APIName, label, source.Parameter)
			}
		case ValueSourceStatic:
			if source.Static == "" {
				return fmt.Errorf("action %q %s is static but has no value", action.APIName, label)
			}
		case ValueSourceCurrentUser, ValueSourceCurrentTime:
		default:
			return fmt.Errorf("action %q %s has unknown value source kind %q", action.APIName, label, source.Kind)
		}
		return nil
	}

	switch rule.Kind {
	case ActionRuleCreateObject, ActionRuleModifyObject, ActionRuleCreateOrModifyObject, ActionRuleDeleteObject:
		objectType, ok := compiled.objectType(rule.ObjectType)
		if !ok {
			return fmt.Errorf("action %q rule references unknown object type %q", action.APIName, rule.ObjectType)
		}
		for propertyName, source := range rule.PropertyValues {
			if _, ok := compiled.property(objectType.APIName, propertyName); !ok {
				return fmt.Errorf("action %q rule sets property %q which object type %q does not declare",
					action.APIName, propertyName, objectType.APIName)
			}
			if err := checkSource(source, fmt.Sprintf("rule property %q", propertyName)); err != nil {
				return err
			}
		}
		if rule.Kind == ActionRuleCreateObject || rule.Kind == ActionRuleCreateOrModifyObject {
			found := false
			for propertyName := range rule.PropertyValues {
				if ontologyAPIKey(propertyName) == ontologyAPIKey(objectType.PrimaryKey) {
					found = true
					break
				}
			}
			if !found {
				return fmt.Errorf("action %q creates %q but does not supply its primary key %q",
					action.APIName, objectType.APIName, objectType.PrimaryKey)
			}
		}
		if rule.Kind == ActionRuleModifyObject || rule.Kind == ActionRuleDeleteObject {
			if strings.TrimSpace(rule.Target) == "" {
				return fmt.Errorf("action %q rule %q needs target naming an object reference parameter", action.APIName, rule.Kind)
			}
			if _, ok := parameters[ontologyAPIKey(rule.Target)]; !ok {
				return fmt.Errorf("action %q rule target %q is not a parameter", action.APIName, rule.Target)
			}
		}
		return nil

	case ActionRuleCreateLink, ActionRuleDeleteLink:
		linkType, ok := compiled.linkType(rule.LinkType)
		if !ok {
			return fmt.Errorf("action %q rule references unknown link type %q", action.APIName, rule.LinkType)
		}
		// Foundry restricts link rules to many-to-many links: a link with a
		// ONE side is backed by a foreign key, so it must be edited through
		// modify_object instead.
		if linkType.A.Cardinality != OntologyCardinalityMany || linkType.B.Cardinality != OntologyCardinalityMany {
			return fmt.Errorf("action %q uses a link rule on %q, but link rules only apply to many-to-many links; modify the foreign key property instead",
				action.APIName, linkType.APIName)
		}
		if err := checkSource(rule.From, "link rule from"); err != nil {
			return err
		}
		return checkSource(rule.To, "link rule to")

	default:
		return fmt.Errorf("action %q has unknown rule kind %q", action.APIName, rule.Kind)
	}
}
```

在 `ontology_validation.go` 的 `validateOntologySchema` 末尾，接口校验之后加上：

```go
	if err := validateOntologyActionTypes(schema, compiled); err != nil {
		return err
	}
	if err := validateOntologyObjectSets(schema, compiled); err != nil {
		return err
	}
	return nil
}
```

并在 `objectset_types.go` 追加：

```go
func validateOntologyObjectSets(schema OntologySchema, compiled *compiledOntology) error {
	seen := make(map[string]struct{}, len(schema.ObjectSets))
	for _, named := range schema.ObjectSets {
		if err := validateOntologyAPIName("object set", named.APIName); err != nil {
			return err
		}
		key := ontologyAPIKey(named.APIName)
		if _, exists := seen[key]; exists {
			return fmt.Errorf("duplicate object set %q", named.APIName)
		}
		seen[key] = struct{}{}
		if err := validateObjectSet(named.Definition, 0); err != nil {
			return fmt.Errorf("object set %q: %w", named.APIName, err)
		}
	}
	_ = compiled
	return nil
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./pkg/cortexdb -run 'TestValidateSchema' -v`
Expected: 本任务 7 个新测试 PASS，第 1/3 期已有的 `TestValidateSchema*` 仍 PASS。

- [ ] **Step 5: Commit**

```bash
git add pkg/cortexdb/ontology_action_types.go pkg/cortexdb/ontology_validation.go \
        pkg/cortexdb/objectset_types.go pkg/cortexdb/ontology_action_types_test.go
git commit -m "feat(action): define action types with rules, parameters and submission criteria"
```

---

### Task 17: 提交条件求值与 validate-only

**Files:**
- Create: `pkg/cortexdb/ontology_action_apply.go`
- Test: `pkg/cortexdb/ontology_action_validate_test.go`

- [ ] **Step 1: 写失败的测试**

```go
package cortexdb

import (
	"context"
	"strings"
	"testing"
)

func activateAviationActions(t *testing.T, db *DB) {
	t.Helper()
	if _, err := db.SaveOntologySchema(context.Background(), OntologySaveRequest{
		Schema:   aviationSchemaWithActions(),
		Activate: true,
	}); err != nil {
		t.Fatalf("activate: %v", err)
	}
}

func TestApplyActionValidateOnlyReportsValid(t *testing.T) {
	db := openOntologyTestDB(t)
	activateAviationActions(t, db)

	resp, err := db.ApplyAction(context.Background(), ActionApplyRequest{
		Action:       "registerAirport",
		Parameters:   map[string]string{"iataCode": "LHR", "airportName": "London Heathrow"},
		ValidateOnly: true,
	})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if !resp.Valid {
		t.Fatalf("expected VALID, got %v", resp.Errors)
	}
	if len(resp.Edits) != 0 {
		t.Fatal("validate-only must not report edits")
	}

	// Nothing may have been written.
	if _, err := db.graph.GetNode(context.Background(), ontologyNodeID("Airport", "LHR")); err == nil {
		t.Fatal("validate-only must not write to the graph")
	}
}

func TestApplyActionValidateOnlyFailsSubmissionCriterion(t *testing.T) {
	db := openOntologyTestDB(t)
	activateAviationActions(t, db)

	resp, err := db.ApplyAction(context.Background(), ActionApplyRequest{
		Action:       "registerAirport",
		Parameters:   map[string]string{"iataCode": "heathrow", "airportName": "London Heathrow"},
		ValidateOnly: true,
	})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if resp.Valid {
		t.Fatal("expected INVALID for a lowercase IATA code")
	}
	if len(resp.Errors) == 0 || !strings.Contains(resp.Errors[0], "three uppercase") {
		t.Fatalf("expected the configured failure message, got %v", resp.Errors)
	}
}

func TestApplyActionRejectsMissingRequiredParameter(t *testing.T) {
	db := openOntologyTestDB(t)
	activateAviationActions(t, db)

	resp, err := db.ApplyAction(context.Background(), ActionApplyRequest{
		Action:       "registerAirport",
		Parameters:   map[string]string{"iataCode": "LHR"},
		ValidateOnly: true,
	})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if resp.Valid {
		t.Fatal("expected a missing required parameter to be INVALID")
	}
	if len(resp.Errors) == 0 || !strings.Contains(resp.Errors[0], "airportName") {
		t.Fatalf("expected the missing parameter to be named, got %v", resp.Errors)
	}
}

func TestApplyActionRejectsWrongParameterDataType(t *testing.T) {
	db := openOntologyTestDB(t)

	schema := aviationSchemaWithActions()
	schema.ActionTypes[0].Parameters = append(schema.ActionTypes[0].Parameters, OntologyActionParameter{
		APIName: "elevation", DataType: OntologyDataType{Kind: OntologyDataInteger},
	})
	if _, err := db.SaveOntologySchema(context.Background(), OntologySaveRequest{Schema: schema, Activate: true}); err != nil {
		t.Fatalf("activate: %v", err)
	}

	resp, err := db.ApplyAction(context.Background(), ActionApplyRequest{
		Action:       "registerAirport",
		Parameters:   map[string]string{"iataCode": "LHR", "airportName": "Heathrow", "elevation": "high"},
		ValidateOnly: true,
	})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if resp.Valid {
		t.Fatal("expected a type violation to be INVALID")
	}
}

func TestApplyActionRejectsUnknownAction(t *testing.T) {
	db := openOntologyTestDB(t)
	activateAviationActions(t, db)

	if _, err := db.ApplyAction(context.Background(), ActionApplyRequest{Action: "nope"}); err == nil {
		t.Fatal("expected an unknown action to be an error, not an INVALID result")
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./pkg/cortexdb -run TestApplyAction -v`
Expected: `undefined: (*DB).ApplyAction`。

- [ ] **Step 3: 写实现**

```go
package cortexdb

import (
	"context"
	"fmt"
	"regexp"
	"strings"
)

// ActionApplyRequest runs one action type.
type ActionApplyRequest struct {
	Action     string            `json:"action"`
	Parameters map[string]string `json:"parameters,omitempty"`
	// ValidateOnly checks parameters and submission criteria without
	// writing. Mutually exclusive with ReturnEdits, matching OSDK.
	ValidateOnly bool `json:"validate_only,omitempty"`
	// ReturnEdits includes the graph edits the action made.
	ReturnEdits bool `json:"return_edits,omitempty"`
	// Actor is recorded in the audit trail and resolves current_user value
	// sources.
	Actor string `json:"actor,omitempty"`
}

// ActionEdit is one graph change an action made.
type ActionEdit struct {
	Kind       string `json:"kind"`
	ObjectID   string `json:"object_id,omitempty"`
	ObjectType string `json:"object_type,omitempty"`
	LinkType   string `json:"link_type,omitempty"`
	FromID     string `json:"from_id,omitempty"`
	ToID       string `json:"to_id,omitempty"`
}

// ActionApplyResponse reports validity and, when asked, the edits applied.
type ActionApplyResponse struct {
	Action  string       `json:"action"`
	Valid   bool         `json:"valid"`
	Applied bool         `json:"applied"`
	Errors  []string     `json:"errors,omitempty"`
	Edits   []ActionEdit `json:"edits,omitempty"`
}

func (db *DB) ApplyAction(ctx context.Context, req ActionApplyRequest) (*ActionApplyResponse, error) {
	if req.ValidateOnly && req.ReturnEdits {
		return nil, fmt.Errorf("validate_only and return_edits cannot both be true")
	}
	compiled, err := db.activeCompiledOntology(ctx)
	if err != nil {
		return nil, err
	}
	if compiled == nil {
		return nil, fmt.Errorf("no active ontology defines any actions")
	}

	action, ok := compiled.actionType(req.Action)
	if !ok {
		return nil, fmt.Errorf("ontology defines no action type %q", req.Action)
	}

	failures := validateActionParameters(action, req.Parameters)
	failures = append(failures, evaluateSubmissionCriteria(action, req.Parameters)...)

	response := &ActionApplyResponse{Action: action.APIName, Valid: len(failures) == 0, Errors: failures}
	if !response.Valid || req.ValidateOnly {
		return response, nil
	}

	edits, err := db.applyActionRules(ctx, compiled, action, req)
	if err != nil {
		return nil, err
	}
	response.Applied = true
	if req.ReturnEdits {
		response.Edits = edits
	}
	if err := db.recordActionAudit(ctx, action, req, edits); err != nil {
		return nil, err
	}
	return response, nil
}

// validateActionParameters checks presence, allowed values and data types.
// Like Foundry's Validate Action it never consults the graph, so it cannot
// and does not check primary key uniqueness.
func validateActionParameters(action OntologyActionType, supplied map[string]string) []string {
	normalized := make(map[string]string, len(supplied))
	for key, value := range supplied {
		normalized[ontologyAPIKey(key)] = value
	}

	failures := make([]string, 0)
	declared := make(map[string]struct{}, len(action.Parameters))
	for _, parameter := range action.Parameters {
		key := ontologyAPIKey(parameter.APIName)
		declared[key] = struct{}{}

		value, present := normalized[key]
		if !present || strings.TrimSpace(value) == "" {
			if parameter.Required {
				failures = append(failures, fmt.Sprintf("parameter %q is required", parameter.APIName))
			}
			continue
		}
		if err := parseOntologyPropertyValue(parameter.DataType, value); err != nil {
			failures = append(failures, fmt.Sprintf("parameter %q: %v", parameter.APIName, err))
			continue
		}
		if len(parameter.AllowedValues) > 0 {
			allowed := false
			for _, candidate := range parameter.AllowedValues {
				if candidate == value {
					allowed = true
					break
				}
			}
			if !allowed {
				failures = append(failures, fmt.Sprintf("parameter %q value %q is not one of the allowed values", parameter.APIName, value))
			}
		}
	}

	for key := range normalized {
		if _, ok := declared[key]; !ok {
			failures = append(failures, fmt.Sprintf("action does not declare parameter %q", key))
		}
	}
	return failures
}

func evaluateSubmissionCriteria(action OntologyActionType, supplied map[string]string) []string {
	normalized := make(map[string]string, len(supplied))
	for key, value := range supplied {
		normalized[ontologyAPIKey(key)] = value
	}

	failures := make([]string, 0)
	for _, criterion := range action.SubmissionCriteria {
		value := normalized[ontologyAPIKey(criterion.Parameter)]

		if criterion.Regex != "" {
			pattern, err := regexp.Compile(criterion.Regex)
			if err != nil {
				failures = append(failures, fmt.Sprintf("submission criterion on %q has an invalid regex: %v", criterion.Parameter, err))
				continue
			}
			if !pattern.MatchString(value) {
				failures = append(failures, submissionFailureMessage(criterion,
					fmt.Sprintf("parameter %q does not match %s", criterion.Parameter, criterion.Regex)))
			}
			continue
		}
		if criterion.Op == "" {
			continue
		}
		matched, err := matchScalarPredicate(ObjectSetPredicate{
			Op: criterion.Op, Property: criterion.Parameter, Value: criterion.Value, Values: criterion.Values,
		}, value, value != "")
		if err != nil {
			failures = append(failures, fmt.Sprintf("submission criterion on %q: %v", criterion.Parameter, err))
			continue
		}
		if !matched {
			failures = append(failures, submissionFailureMessage(criterion,
				fmt.Sprintf("parameter %q failed the %s check", criterion.Parameter, criterion.Op)))
		}
	}
	return failures
}

func submissionFailureMessage(criterion OntologySubmissionCriterion, fallback string) string {
	if strings.TrimSpace(criterion.FailureMessage) != "" {
		return criterion.FailureMessage
	}
	return fallback
}
```

在 `ontology_compile.go` 追加：

```go
func (c *compiledOntology) actionType(apiName string) (OntologyActionType, bool) {
	key := ontologyAPIKey(apiName)
	for _, action := range c.schema.ActionTypes {
		if ontologyAPIKey(action.APIName) == key {
			return action, true
		}
	}
	return OntologyActionType{}, false
}
```

本任务里 `applyActionRules` 和 `recordActionAudit` 写成返回 `nil, nil` / `nil` 的桩，Task 18 填实现。

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./pkg/cortexdb -run TestApplyAction -v`
Expected: PASS ×5。

- [ ] **Step 5: Commit**

```bash
git add pkg/cortexdb/ontology_action_apply.go pkg/cortexdb/ontology_compile.go \
        pkg/cortexdb/ontology_action_validate_test.go
git commit -m "feat(action): validate action parameters and submission criteria"
```

---

### Task 18: 执行规则、返回 edits、审计落库

**Files:**
- Modify: `pkg/cortexdb/ontology_action_apply.go`（替换两个桩）
- Test: `pkg/cortexdb/ontology_action_apply_test.go`

- [ ] **Step 1: 写失败的测试**

```go
package cortexdb

import (
	"context"
	"strings"
	"testing"
)

func TestApplyActionCreatesObject(t *testing.T) {
	db := openOntologyTestDB(t)
	activateAviationActions(t, db)
	ctx := context.Background()

	resp, err := db.ApplyAction(ctx, ActionApplyRequest{
		Action:      "registerAirport",
		Parameters:  map[string]string{"iataCode": "LHR", "airportName": "London Heathrow"},
		ReturnEdits: true,
		Actor:       "tester",
	})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if !resp.Applied {
		t.Fatalf("expected the action to apply, errors: %v", resp.Errors)
	}
	if len(resp.Edits) != 1 || resp.Edits[0].Kind != string(ActionRuleCreateObject) {
		t.Fatalf("expected one create edit, got %+v", resp.Edits)
	}

	node, err := db.graph.GetNode(ctx, ontologyNodeID("Airport", "LHR"))
	if err != nil {
		t.Fatalf("expected the airport to exist: %v", err)
	}
	if node.NodeType != "Airport" {
		t.Fatalf("unexpected node type %q", node.NodeType)
	}
}

func TestApplyActionWithoutReturnEditsHidesEdits(t *testing.T) {
	db := openOntologyTestDB(t)
	activateAviationActions(t, db)

	resp, err := db.ApplyAction(context.Background(), ActionApplyRequest{
		Action:     "registerAirport",
		Parameters: map[string]string{"iataCode": "LGW", "airportName": "Gatwick"},
	})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if !resp.Applied {
		t.Fatal("expected the action to apply")
	}
	if len(resp.Edits) != 0 {
		t.Fatal("edits must be withheld unless return_edits is set")
	}
}

func TestApplyActionWritesAuditTrail(t *testing.T) {
	db := openOntologyTestDB(t)
	activateAviationActions(t, db)
	ctx := context.Background()

	if _, err := db.ApplyAction(ctx, ActionApplyRequest{
		Action:     "registerAirport",
		Parameters: map[string]string{"iataCode": "LHR", "airportName": "London Heathrow"},
		Actor:      "tester",
	}); err != nil {
		t.Fatalf("apply: %v", err)
	}

	var (
		count int
		actor string
	)
	row := db.store.GetDB().QueryRowContext(ctx,
		`SELECT COUNT(*), COALESCE(MAX(actor), '') FROM ontology_action_audit WHERE action_api_name = ?`,
		"registerAirport")
	if err := row.Scan(&count, &actor); err != nil {
		t.Fatalf("scan audit: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected 1 audit row, got %d", count)
	}
	if actor != "tester" {
		t.Fatalf("expected the actor to be recorded, got %q", actor)
	}
}

func TestApplyActionModifyObject(t *testing.T) {
	db := openOntologyTestDB(t)
	ctx := context.Background()

	schema := aviationSchemaWithActions()
	schema.ActionTypes = append(schema.ActionTypes, OntologyActionType{
		APIName: "renameAirport",
		Parameters: []OntologyActionParameter{
			{APIName: "airport", DataType: OntologyDataType{Kind: OntologyDataString}, Required: true, ObjectType: "Airport"},
			{APIName: "newName", DataType: OntologyDataType{Kind: OntologyDataString}, Required: true},
		},
		Rules: []OntologyActionRule{
			{
				Kind:       ActionRuleModifyObject,
				ObjectType: "Airport",
				Target:     "airport",
				PropertyValues: map[string]OntologyValueSource{
					"airportName": {Kind: ValueSourceParameter, Parameter: "newName"},
				},
			},
		},
	})
	if _, err := db.SaveOntologySchema(ctx, OntologySaveRequest{Schema: schema, Activate: true}); err != nil {
		t.Fatalf("activate: %v", err)
	}

	if _, err := db.ApplyAction(ctx, ActionApplyRequest{
		Action:     "registerAirport",
		Parameters: map[string]string{"iataCode": "LHR", "airportName": "Heathrow"},
	}); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := db.ApplyAction(ctx, ActionApplyRequest{
		Action: "renameAirport",
		Parameters: map[string]string{
			"airport": ontologyNodeID("Airport", "LHR"),
			"newName": "London Heathrow",
		},
	}); err != nil {
		t.Fatalf("modify: %v", err)
	}

	node, err := db.graph.GetNode(ctx, ontologyNodeID("Airport", "LHR"))
	if err != nil {
		t.Fatalf("get node: %v", err)
	}
	if fmt.Sprintf("%v", node.Properties["airportName"]) != "London Heathrow" {
		t.Fatalf("expected the rename to apply, got %v", node.Properties["airportName"])
	}
}

func TestApplyActionRejectsModifyOfObjectCreatedInSameAction(t *testing.T) {
	db := openOntologyTestDB(t)
	ctx := context.Background()

	schema := aviationSchemaWithActions()
	// One action that both creates and then modifies the same object.
	schema.ActionTypes[0].Rules = append(schema.ActionTypes[0].Rules, OntologyActionRule{
		Kind:       ActionRuleModifyObject,
		ObjectType: "Airport",
		Target:     "iataCode",
		PropertyValues: map[string]OntologyValueSource{
			"airportName": {Kind: ValueSourceStatic, Static: "renamed"},
		},
	})
	if _, err := db.SaveOntologySchema(ctx, OntologySaveRequest{Schema: schema, Activate: true}); err != nil {
		t.Fatalf("activate: %v", err)
	}

	_, err := db.ApplyAction(ctx, ActionApplyRequest{
		Action:     "registerAirport",
		Parameters: map[string]string{"iataCode": "LHR", "airportName": "Heathrow"},
	})
	if err == nil || !strings.Contains(err.Error(), "created in the same action") {
		t.Fatalf("expected Foundry's same-action restriction to be enforced, got %v", err)
	}
}
```

测试文件顶部 import 需加 `"fmt"`。

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./pkg/cortexdb -run 'TestApplyActionCreates|TestApplyActionWithout|TestApplyActionWrites|TestApplyActionModify|TestApplyActionRejectsModify' -v`
Expected: 对象未创建、审计表不存在。

- [ ] **Step 3: 写实现**

替换 `ontology_action_apply.go` 里的两个桩：

```go
// applyActionRules executes an action's rules in order. Objects created by
// this action are tracked so that a later modify or delete rule targeting
// them can be refused, matching Foundry's restriction.
func (db *DB) applyActionRules(ctx context.Context, compiled *compiledOntology, action OntologyActionType, req ActionApplyRequest) ([]ActionEdit, error) {
	parameters := make(map[string]string, len(req.Parameters))
	for key, value := range req.Parameters {
		parameters[ontologyAPIKey(key)] = value
	}

	createdThisAction := make(map[string]struct{})
	edits := make([]ActionEdit, 0, len(action.Rules))

	for _, rule := range action.Rules {
		switch rule.Kind {
		case ActionRuleCreateObject, ActionRuleCreateOrModifyObject:
			edit, err := db.applyCreateObjectRule(ctx, compiled, rule, parameters, req)
			if err != nil {
				return nil, err
			}
			createdThisAction[edit.ObjectID] = struct{}{}
			edits = append(edits, edit)

		case ActionRuleModifyObject:
			objectID := parameters[ontologyAPIKey(rule.Target)]
			if _, created := createdThisAction[objectID]; created {
				return nil, fmt.Errorf("action %q modifies an object created in the same action, which is not allowed", action.APIName)
			}
			edit, err := db.applyModifyObjectRule(ctx, compiled, rule, objectID, parameters, req)
			if err != nil {
				return nil, err
			}
			edits = append(edits, edit)

		case ActionRuleDeleteObject:
			objectID := parameters[ontologyAPIKey(rule.Target)]
			if _, created := createdThisAction[objectID]; created {
				return nil, fmt.Errorf("action %q deletes an object created in the same action, which is not allowed", action.APIName)
			}
			if err := db.graph.DeleteNode(ctx, objectID); err != nil {
				return nil, fmt.Errorf("action %q delete object: %w", action.APIName, err)
			}
			edits = append(edits, ActionEdit{Kind: string(rule.Kind), ObjectID: objectID, ObjectType: rule.ObjectType})

		case ActionRuleCreateLink, ActionRuleDeleteLink:
			edit, err := db.applyLinkRule(ctx, compiled, rule, parameters, req)
			if err != nil {
				return nil, err
			}
			edits = append(edits, edit)

		default:
			return nil, fmt.Errorf("action %q has unsupported rule kind %q", action.APIName, rule.Kind)
		}
	}
	return edits, nil
}

func (db *DB) applyCreateObjectRule(ctx context.Context, compiled *compiledOntology, rule OntologyActionRule, parameters map[string]string, req ActionApplyRequest) (ActionEdit, error) {
	objectType, _ := compiled.objectType(rule.ObjectType)

	properties := make(map[string]string, len(rule.PropertyValues))
	for propertyName, source := range rule.PropertyValues {
		value, err := resolveActionValue(source, parameters, req)
		if err != nil {
			return ActionEdit{}, err
		}
		properties[propertyName] = value
	}

	entity := ToolEntityInput{
		Name:     properties[objectType.TitleProperty],
		Type:     objectType.APIName,
		Metadata: properties,
	}
	if entity.Name == "" {
		entity.Name = properties[objectType.PrimaryKey]
	}
	if err := validateOntologyEntity(compiled, entity); err != nil {
		return ActionEdit{}, err
	}
	if _, err := db.GraphRAGTools().UpsertEntities(ctx, ToolUpsertEntitiesRequest{
		Entities: []ToolEntityInput{entity},
	}); err != nil {
		return ActionEdit{}, fmt.Errorf("action create object: %w", err)
	}

	nodeID, err := ontologyEntityNodeID(compiled, entity)
	if err != nil {
		return ActionEdit{}, err
	}
	return ActionEdit{Kind: string(rule.Kind), ObjectID: nodeID, ObjectType: objectType.APIName}, nil
}

func (db *DB) applyModifyObjectRule(ctx context.Context, compiled *compiledOntology, rule OntologyActionRule, objectID string, parameters map[string]string, req ActionApplyRequest) (ActionEdit, error) {
	node, err := db.graph.GetNode(ctx, objectID)
	if err != nil {
		return ActionEdit{}, fmt.Errorf("action modify object %s: %w", objectID, err)
	}
	if node.Properties == nil {
		node.Properties = map[string]interface{}{}
	}
	for propertyName, source := range rule.PropertyValues {
		property, ok := compiled.property(rule.ObjectType, propertyName)
		if !ok {
			return ActionEdit{}, fmt.Errorf("object type %q has no property %q", rule.ObjectType, propertyName)
		}
		value, err := resolveActionValue(source, parameters, req)
		if err != nil {
			return ActionEdit{}, err
		}
		if err := parseOntologyPropertyValue(property.DataType, value); err != nil {
			return ActionEdit{}, fmt.Errorf("property %q: %w", property.APIName, err)
		}
		node.Properties[property.APIName] = value
	}
	if err := db.graph.UpsertNode(ctx, node); err != nil {
		return ActionEdit{}, fmt.Errorf("action modify object: %w", err)
	}
	return ActionEdit{Kind: string(rule.Kind), ObjectID: objectID, ObjectType: rule.ObjectType}, nil
}

func (db *DB) applyLinkRule(ctx context.Context, compiled *compiledOntology, rule OntologyActionRule, parameters map[string]string, req ActionApplyRequest) (ActionEdit, error) {
	linkType, _ := compiled.linkType(rule.LinkType)
	fromID, err := resolveActionValue(rule.From, parameters, req)
	if err != nil {
		return ActionEdit{}, err
	}
	toID, err := resolveActionValue(rule.To, parameters, req)
	if err != nil {
		return ActionEdit{}, err
	}

	if rule.Kind == ActionRuleCreateLink {
		if _, err := db.GraphRAGTools().UpsertRelations(ctx, ToolUpsertRelationsRequest{
			Relations: []ToolRelationInput{{From: fromID, To: toID, Type: linkType.APIName}},
		}); err != nil {
			return ActionEdit{}, fmt.Errorf("action create link: %w", err)
		}
	} else {
		if _, err := db.store.GetDB().ExecContext(ctx,
			`DELETE FROM graph_edges WHERE edge_type = ? AND from_node_id = ? AND to_node_id = ?`,
			linkType.APIName, fromID, toID); err != nil {
			return ActionEdit{}, fmt.Errorf("action delete link: %w", err)
		}
	}
	return ActionEdit{Kind: string(rule.Kind), LinkType: linkType.APIName, FromID: fromID, ToID: toID}, nil
}

func resolveActionValue(source OntologyValueSource, parameters map[string]string, req ActionApplyRequest) (string, error) {
	switch source.Kind {
	case ValueSourceParameter:
		return parameters[ontologyAPIKey(source.Parameter)], nil
	case ValueSourceStatic:
		return source.Static, nil
	case ValueSourceCurrentUser:
		return req.Actor, nil
	case ValueSourceCurrentTime:
		return actionTimestamp(), nil
	case ValueSourceObjectProperty:
		// The object reference parameter carries the node ID; reading a
		// property off it is resolved by the caller that has graph access.
		return parameters[ontologyAPIKey(source.Parameter)], nil
	default:
		return "", fmt.Errorf("unknown value source kind %q", source.Kind)
	}
}
```

`actionTimestamp` 和审计表：

```go
func actionTimestamp() string {
	return time.Now().UTC().Format(time.RFC3339)
}

func (db *DB) ensureActionAuditTable(ctx context.Context) error {
	db.actionAuditInit.Do(func() {
		_, db.actionAuditInitErr = db.store.GetDB().ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS ontology_action_audit (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			action_api_name TEXT NOT NULL,
			actor TEXT,
			parameters TEXT NOT NULL,
			edits TEXT NOT NULL,
			applied_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		);
		CREATE INDEX IF NOT EXISTS idx_ontology_action_audit_action ON ontology_action_audit(action_api_name);
		`)
	})
	return db.actionAuditInitErr
}

// recordActionAudit writes what was changed, by whom, with what inputs.
// This is the point of routing writes through actions rather than through a
// generic upsert: the trail exists whether or not anyone asked for it.
func (db *DB) recordActionAudit(ctx context.Context, action OntologyActionType, req ActionApplyRequest, edits []ActionEdit) error {
	if err := db.ensureActionAuditTable(ctx); err != nil {
		return fmt.Errorf("init action audit table: %w", err)
	}
	parametersJSON, err := json.Marshal(req.Parameters)
	if err != nil {
		return fmt.Errorf("encode action parameters: %w", err)
	}
	editsJSON, err := json.Marshal(edits)
	if err != nil {
		return fmt.Errorf("encode action edits: %w", err)
	}
	if _, err := db.store.GetDB().ExecContext(ctx,
		`INSERT INTO ontology_action_audit (action_api_name, actor, parameters, edits) VALUES (?, ?, ?, ?)`,
		action.APIName, req.Actor, string(parametersJSON), string(editsJSON)); err != nil {
		return fmt.Errorf("record action audit: %w", err)
	}
	return nil
}
```

在 `pkg/cortexdb/cortexdb.go` 的 `DB` 结构体里（`ontologySchemaInit` 旁，约 25-26 行）加两个字段：

```go
	actionAuditInit    sync.Once
	actionAuditInitErr error
```

`ontology_action_apply.go` 顶部 import 补 `"encoding/json"`、`"time"`。

先跑 `grep -n "func (g \*GraphStore) \(UpsertNode\|DeleteNode\|GetNode\)" pkg/graph/graph.go` 核对图写入方法的实际名字与签名，据此调整 `applyModifyObjectRule` / delete 分支。

- [ ] **Step 4: 跑测试确认通过**

Run: `go test -race ./pkg/cortexdb`
Expected: 全部 PASS。

- [ ] **Step 5: Commit**

```bash
git add pkg/cortexdb/ontology_action_apply.go pkg/cortexdb/cortexdb.go \
        pkg/cortexdb/ontology_action_apply_test.go
git commit -m "feat(action): execute action rules with edit reporting and an audit trail"
```

---

### Task 19: strict_actions 开关 + 工具面

**Files:**
- Modify: `pkg/cortexdb/graphrag_tool_ingest.go`（`UpsertEntities` / `UpsertRelations` 入口加闸）
- Modify: `pkg/cortexdb/ontology_api.go`、`ontology_tooldefs.go`、`ontology_dispatch.go`、`ontology_mcp.go`
- Test: `pkg/cortexdb/ontology_strict_actions_test.go`

- [ ] **Step 1: 写失败的测试**

```go
package cortexdb

import (
	"context"
	"strings"
	"testing"
)

func TestStrictActionsClosesGenericUpsert(t *testing.T) {
	db := openOntologyTestDB(t)
	ctx := context.Background()

	schema := aviationSchemaWithActions()
	schema.StrictActions = true
	if _, err := db.SaveOntologySchema(ctx, OntologySaveRequest{Schema: schema, Activate: true}); err != nil {
		t.Fatalf("activate: %v", err)
	}

	_, err := db.GraphRAGTools().UpsertEntities(ctx, ToolUpsertEntitiesRequest{Entities: []ToolEntityInput{
		{Name: "London Heathrow", Type: "Airport", Metadata: map[string]string{"iataCode": "LHR"}},
	}})
	if err == nil || !strings.Contains(err.Error(), "strict_actions") {
		t.Fatalf("expected generic upsert to be closed under strict_actions, got %v", err)
	}

	// Actions must still work.
	resp, err := db.ApplyAction(ctx, ActionApplyRequest{
		Action:     "registerAirport",
		Parameters: map[string]string{"iataCode": "LHR", "airportName": "London Heathrow"},
	})
	if err != nil {
		t.Fatalf("action under strict_actions: %v", err)
	}
	if !resp.Applied {
		t.Fatalf("expected the action to apply, errors: %v", resp.Errors)
	}
}

func TestStrictActionsDefaultsOff(t *testing.T) {
	db := openOntologyTestDB(t)
	ctx := context.Background()

	if _, err := db.SaveOntologySchema(ctx, OntologySaveRequest{
		Schema: aviationSchemaWithActions(), Activate: true,
	}); err != nil {
		t.Fatalf("activate: %v", err)
	}

	if _, err := db.GraphRAGTools().UpsertEntities(ctx, ToolUpsertEntitiesRequest{Entities: []ToolEntityInput{
		{Name: "London Heathrow", Type: "Airport", Metadata: map[string]string{"iataCode": "LHR"}},
	}}); err != nil {
		t.Fatalf("generic upsert must stay open by default, got %v", err)
	}
}

func TestActionToolsAreReachable(t *testing.T) {
	db := openOntologyTestDB(t)
	activateAviationActions(t, db)
	ctx := context.Background()

	listed, err := db.GraphRAGTools().ListActionTypes(ctx, ActionListRequest{})
	if err != nil {
		t.Fatalf("list actions: %v", err)
	}
	if len(listed.Actions) != 1 || listed.Actions[0].APIName != "registerAirport" {
		t.Fatalf("unexpected action list: %+v", listed.Actions)
	}

	applied, err := db.GraphRAGTools().ApplyAction(ctx, ActionApplyRequest{
		Action:       "registerAirport",
		Parameters:   map[string]string{"iataCode": "LHR", "airportName": "London Heathrow"},
		ValidateOnly: true,
	})
	if err != nil {
		t.Fatalf("apply via toolbox: %v", err)
	}
	if !applied.Valid {
		t.Fatalf("expected VALID, got %v", applied.Errors)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./pkg/cortexdb -run 'TestStrictActions|TestActionToolsAreReachable' -v`
Expected: `undefined: ActionListRequest`；strict 测试未拦截。

- [ ] **Step 3: 写实现**

在 `ontology_action_apply.go` 追加闸门与列表 API：

```go
// guardStrictActions closes the generic upsert path when the active schema
// asks for it. Off by default, so existing callers keep working unchanged.
func (db *DB) guardStrictActions(ctx context.Context) error {
	schema, err := db.loadActiveOntologySchema(ctx)
	if err != nil || schema == nil {
		return err
	}
	if !schema.StrictActions {
		return nil
	}
	if len(schema.ActionTypes) == 0 {
		return nil
	}
	return fmt.Errorf("ontology %q sets strict_actions: write through an action type (see ontology_action_list), not the generic upsert tools", schema.SchemaID)
}

// ActionListRequest lists the action types on the active ontology.
type ActionListRequest struct{}

// ActionListResponse returns the callable action types.
type ActionListResponse struct {
	Actions []OntologyActionType `json:"actions"`
}

func (db *DB) ListActionTypes(ctx context.Context, _ ActionListRequest) (*ActionListResponse, error) {
	schema, err := db.loadActiveOntologySchema(ctx)
	if err != nil {
		return nil, err
	}
	if schema == nil {
		return &ActionListResponse{Actions: []OntologyActionType{}}, nil
	}
	return &ActionListResponse{Actions: schema.ActionTypes}, nil
}

func (t *GraphRAGToolbox) ApplyAction(ctx context.Context, req ActionApplyRequest) (*ActionApplyResponse, error) {
	return t.db.ApplyAction(ctx, req)
}

func (t *GraphRAGToolbox) ListActionTypes(ctx context.Context, req ActionListRequest) (*ActionListResponse, error) {
	return t.db.ListActionTypes(ctx, req)
}
```

在 `graphrag_tool_ingest.go` 的 `UpsertEntities` 和 `UpsertRelations` **最前面**各加：

```go
if err := t.db.guardStrictActions(ctx); err != nil {
	return nil, err
}
```

注意 `applyCreateObjectRule` / `applyLinkRule` 内部调用的也是 `UpsertEntities` / `UpsertRelations`，会被自己的闸门挡住。解决办法：把闸门只加在**工具层**入口，让 action 走内部未加闸的路径。最简做法是在 `DB` 上加一个 `ctx` 标记：

```go
type actionApplyContextKey struct{}

func withinActionApply(ctx context.Context) context.Context {
	return context.WithValue(ctx, actionApplyContextKey{}, true)
}

func isWithinActionApply(ctx context.Context) bool {
	value, _ := ctx.Value(actionApplyContextKey{}).(bool)
	return value
}
```

`guardStrictActions` 开头加 `if isWithinActionApply(ctx) { return nil }`，`applyActionRules` 入口把 `ctx = withinActionApply(ctx)`。

工具定义追加两条：

```go
{
	Name:        "ontology_action_list",
	Description: "List the action types callable on the active ontology, with their parameters, rules and submission criteria. When the schema sets strict_actions, these are the only writes allowed.",
	InputSchema: toolObjectSchema(nil, map[string]any{}),
},
{
	Name:        "ontology_action_apply",
	Description: "Run one action type. Set validate_only to check parameters and submission criteria without writing, or return_edits to get the graph edits back (the two are mutually exclusive). Every applied action is recorded in an audit trail.",
	InputSchema: toolObjectSchema(
		[]string{"action"},
		map[string]any{
			"action":        toolStringSchema("Action type api name."),
			"parameters":    toolMapSchema("Action parameter values, keyed by parameter api name."),
			"validate_only": toolBooleanSchema("Validate without writing."),
			"return_edits":  toolBooleanSchema("Return the graph edits the action made."),
			"actor":         toolStringSchema("Who is running the action; recorded in the audit trail."),
		},
	),
},
```

dispatch 与 MCP 注册照 Task 15 的模式各加两段。

- [ ] **Step 4: 跑测试确认通过**

Run: `go test -race ./pkg/cortexdb`
Expected: 全部 PASS，含 `TestEveryToolDefinitionIsReachableOverMCP`。

- [ ] **Step 5: Commit 第 5 期**

```bash
git add pkg/cortexdb/ontology_action_apply.go pkg/cortexdb/graphrag_tool_ingest.go \
        pkg/cortexdb/ontology_tooldefs.go pkg/cortexdb/ontology_dispatch.go \
        pkg/cortexdb/ontology_mcp.go pkg/cortexdb/ontology_strict_actions_test.go
git commit -m "feat(action): expose action tools and the strict_actions write gate"
```

---

# 第 6 期：类型化工具生成 + schema diff + 发布

OSDK 从 ontology 生成类型化客户端。CortexDB 的等价物是：**从 schema 生成带具体 JSON Schema 的 MCP 工具**，而不是让 LLM 往通用 upsert 里塞自由文本。

⚠️ **照抄 OSDK 1.x → 2.0 的教训**：1.x 为每个实体生成完整实现，代码量随 ontology 大小线性膨胀。我们会撞上同一个问题的变体——**工具数量爆炸吃掉 context window**。所以生成的工具默认**不注册到 MCP**，通过一个显式开关按需暴露，并对数量设上限。

### Task 20: 从 schema 生成类型化工具定义

**Files:**
- Create: `pkg/cortexdb/ontology_sdk.go`
- Test: `pkg/cortexdb/ontology_sdk_test.go`

- [ ] **Step 1: 写失败的测试**

```go
package cortexdb

import (
	"context"
	"testing"
)

func TestGenerateTypedToolsForActions(t *testing.T) {
	db := openOntologyTestDB(t)
	activateAviationActions(t, db)

	definitions, err := db.GenerateOntologyTools(context.Background(), OntologyToolGenOptions{})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	var registerAirport *ToolDefinition
	for i := range definitions {
		if definitions[i].Name == "action_register_airport" {
			registerAirport = &definitions[i]
		}
	}
	if registerAirport == nil {
		t.Fatalf("expected a generated action tool, got %v", toolNames(definitions))
	}

	properties, ok := registerAirport.InputSchema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("generated tool has no properties: %+v", registerAirport.InputSchema)
	}
	if _, ok := properties["iataCode"]; !ok {
		t.Fatalf("expected iataCode as a first-class parameter, got %+v", properties)
	}

	required, ok := registerAirport.InputSchema["required"].([]string)
	if !ok || len(required) != 2 {
		t.Fatalf("expected both required parameters to be declared, got %v", registerAirport.InputSchema["required"])
	}
}

func TestGenerateTypedToolsForObjectTypes(t *testing.T) {
	db := openOntologyTestDB(t)
	activateAviationActions(t, db)

	definitions, err := db.GenerateOntologyTools(context.Background(), OntologyToolGenOptions{IncludeObjectTypes: true})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	names := toolNames(definitions)
	found := false
	for _, name := range names {
		if name == "list_airport" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected a per-object-type list tool, got %v", names)
	}
}

func TestGenerateToolsMapsDataTypesToJSONSchemaTypes(t *testing.T) {
	db := openOntologyTestDB(t)
	ctx := context.Background()

	schema := aviationSchemaWithActions()
	schema.ActionTypes[0].Parameters = append(schema.ActionTypes[0].Parameters,
		OntologyActionParameter{APIName: "elevation", DataType: OntologyDataType{Kind: OntologyDataInteger}},
		OntologyActionParameter{APIName: "isHub", DataType: OntologyDataType{Kind: OntologyDataBoolean}},
	)
	if _, err := db.SaveOntologySchema(ctx, OntologySaveRequest{Schema: schema, Activate: true}); err != nil {
		t.Fatalf("activate: %v", err)
	}

	definitions, err := db.GenerateOntologyTools(ctx, OntologyToolGenOptions{})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	for _, definition := range definitions {
		if definition.Name != "action_register_airport" {
			continue
		}
		properties := definition.InputSchema["properties"].(map[string]any)
		elevation := properties["elevation"].(map[string]any)
		if elevation["type"] != "integer" {
			t.Fatalf("expected integer, got %v", elevation["type"])
		}
		isHub := properties["isHub"].(map[string]any)
		if isHub["type"] != "boolean" {
			t.Fatalf("expected boolean, got %v", isHub["type"])
		}
		return
	}
	t.Fatal("generated tool not found")
}

func TestGenerateToolsRespectsMaxTools(t *testing.T) {
	db := openOntologyTestDB(t)
	activateAviationActions(t, db)

	definitions, err := db.GenerateOntologyTools(context.Background(), OntologyToolGenOptions{
		IncludeObjectTypes: true,
		MaxTools:           1,
	})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if len(definitions) != 1 {
		t.Fatalf("expected the cap to be honoured, got %d tools", len(definitions))
	}
}

func TestGenerateToolsWithoutActiveSchemaReturnsNothing(t *testing.T) {
	db := openOntologyTestDB(t)

	definitions, err := db.GenerateOntologyTools(context.Background(), OntologyToolGenOptions{})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if len(definitions) != 0 {
		t.Fatalf("expected no generated tools without an ontology, got %d", len(definitions))
	}
}

func toolNames(definitions []ToolDefinition) []string {
	names := make([]string, 0, len(definitions))
	for _, definition := range definitions {
		names = append(names, definition.Name)
	}
	return names
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./pkg/cortexdb -run 'TestGenerateTypedTools|TestGenerateTools' -v`
Expected: `undefined: (*DB).GenerateOntologyTools`。

- [ ] **Step 3: 写实现**

```go
package cortexdb

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"unicode"
)

// defaultMaxGeneratedTools bounds how many tools an ontology may expand to.
// OSDK 1.x generated code proportional to the whole ontology and paid for it;
// here the cost lands on the agent's context window instead, so the cap is
// the default rather than an option.
const defaultMaxGeneratedTools = 32

// OntologyToolGenOptions controls tool generation from the active ontology.
type OntologyToolGenOptions struct {
	// IncludeObjectTypes also emits one list tool per object type.
	IncludeObjectTypes bool `json:"include_object_types,omitempty"`
	// MaxTools caps the number of generated tools. Zero uses the default.
	MaxTools int `json:"max_tools,omitempty"`
}

// GenerateOntologyTools turns the active ontology into typed tool
// definitions: one per action type, and optionally one list tool per object
// type. Typed tools beat a generic upsert because the parameter names, types
// and required-ness are in the schema the model sees, not in prose.
func (db *DB) GenerateOntologyTools(ctx context.Context, options OntologyToolGenOptions) ([]ToolDefinition, error) {
	schema, err := db.loadActiveOntologySchema(ctx)
	if err != nil || schema == nil {
		return nil, err
	}

	maxTools := options.MaxTools
	if maxTools <= 0 {
		maxTools = defaultMaxGeneratedTools
	}

	definitions := make([]ToolDefinition, 0, len(schema.ActionTypes))
	for _, action := range schema.ActionTypes {
		definitions = append(definitions, generateActionTool(action))
	}
	if options.IncludeObjectTypes {
		for _, objectType := range schema.ObjectTypes {
			definitions = append(definitions, generateObjectTypeListTool(objectType))
		}
	}
	sort.Slice(definitions, func(i, j int) bool { return definitions[i].Name < definitions[j].Name })

	if len(definitions) > maxTools {
		definitions = definitions[:maxTools]
	}
	return definitions, nil
}

func generateActionTool(action OntologyActionType) ToolDefinition {
	properties := make(map[string]any, len(action.Parameters))
	required := make([]string, 0, len(action.Parameters))

	for _, parameter := range action.Parameters {
		properties[parameter.APIName] = jsonSchemaForDataType(parameter.DataType, parameterDescription(parameter))
		if parameter.Required {
			required = append(required, parameter.APIName)
		}
	}
	sort.Strings(required)

	description := action.Description
	if description == "" {
		description = fmt.Sprintf("Run the %s action.", firstNonEmpty(action.DisplayName, action.APIName))
	}
	if len(action.SubmissionCriteria) > 0 {
		description += " Submission criteria apply; call with validate_only first if unsure."
	}

	return ToolDefinition{
		Name:        "action_" + toSnakeCase(action.APIName),
		Description: description,
		InputSchema: toolObjectSchema(required, properties),
	}
}

func generateObjectTypeListTool(objectType OntologyObjectType) ToolDefinition {
	description := objectType.Description
	if description == "" {
		description = fmt.Sprintf("List %s objects.", firstNonEmpty(objectType.PluralDisplayName, objectType.APIName))
	}
	return ToolDefinition{
		Name:        "list_" + toSnakeCase(objectType.APIName),
		Description: description,
		InputSchema: toolObjectSchema(nil, map[string]any{
			"limit": toolIntegerSchema("Maximum objects to return."),
			"where": map[string]any{
				"type":        "object",
				"description": fmt.Sprintf("Optional filter predicate over %s properties: %s.", objectType.APIName, propertyNameList(objectType)),
			},
		}),
	}
}

func parameterDescription(parameter OntologyActionParameter) string {
	if parameter.Description != "" {
		return parameter.Description
	}
	if parameter.ObjectType != "" {
		return fmt.Sprintf("Node ID of an existing %s object.", parameter.ObjectType)
	}
	return firstNonEmpty(parameter.DisplayName, parameter.APIName)
}

func propertyNameList(objectType OntologyObjectType) string {
	names := make([]string, 0, len(objectType.Properties))
	for _, property := range objectType.Properties {
		names = append(names, property.APIName)
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}

// jsonSchemaForDataType maps an ontology data type onto the JSON Schema the
// model is shown, so a typed ontology yields typed tool parameters.
func jsonSchemaForDataType(dataType OntologyDataType, description string) map[string]any {
	switch dataType.Kind {
	case OntologyDataInteger, OntologyDataLong:
		return map[string]any{"type": "integer", "description": description}
	case OntologyDataDouble, OntologyDataDecimal:
		return map[string]any{"type": "number", "description": description}
	case OntologyDataBoolean:
		return map[string]any{"type": "boolean", "description": description}
	case OntologyDataDate:
		return map[string]any{"type": "string", "description": description + " (YYYY-MM-DD)"}
	case OntologyDataTimestamp:
		return map[string]any{"type": "string", "description": description + " (RFC3339)"}
	case OntologyDataGeoPoint:
		return map[string]any{"type": "string", "description": description + " (\"lat,lon\")"}
	case OntologyDataArray:
		items := map[string]any{"type": "string"}
		if dataType.ItemType != nil {
			items = jsonSchemaForDataType(*dataType.ItemType, "")
		}
		return map[string]any{"type": "array", "items": items, "description": description}
	case OntologyDataStruct:
		return map[string]any{"type": "object", "description": description}
	default:
		return map[string]any{"type": "string", "description": description}
	}
}

// toSnakeCase turns an ontology API name into a tool name segment:
// registerAirport -> register_airport.
func toSnakeCase(value string) string {
	var b strings.Builder
	for i, r := range value {
		if unicode.IsUpper(r) {
			if i > 0 {
				b.WriteRune('_')
			}
			b.WriteRune(unicode.ToLower(r))
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./pkg/cortexdb -run 'TestGenerateTypedTools|TestGenerateTools' -v`
Expected: PASS ×5。若 `ToolDefinition.InputSchema` 的 `required` 不是 `[]string`（跑 `grep -n "func toolObjectSchema" -A 14 pkg/cortexdb/graphrag_tool_defs.go` 确认），据此改测试断言。

- [ ] **Step 5: Commit**

```bash
git add pkg/cortexdb/ontology_sdk.go pkg/cortexdb/ontology_sdk_test.go
git commit -m "feat(ontology): generate typed tool definitions from the active schema"
```

---

### Task 21: schema diff 与破坏性变更检测

**Files:**
- Create: `pkg/cortexdb/ontology_diff.go`
- Modify: `pkg/cortexdb/ontology_api.go`、`ontology_tooldefs.go`、`ontology_dispatch.go`、`ontology_mcp.go`
- Test: `pkg/cortexdb/ontology_diff_test.go`

Foundry 的 merge check 会在合并前验证变更。我们不做分支，但**破坏性变更检测**是其中真正有用的内核。

- [ ] **Step 1: 写失败的测试**

```go
package cortexdb

import "testing"

func TestDiffDetectsAddedObjectType(t *testing.T) {
	before := validAviationSchema()
	after := validAviationSchema()
	after.ObjectTypes = append(after.ObjectTypes, OntologyObjectType{
		APIName: "Aircraft", PrimaryKey: "tailNumber",
		Properties: []OntologyProperty{
			{APIName: "tailNumber", DataType: OntologyDataType{Kind: OntologyDataString}, Required: true},
		},
	})

	diff := DiffOntologySchemas(before, after)
	if len(diff.Changes) != 1 {
		t.Fatalf("expected 1 change, got %+v", diff.Changes)
	}
	if diff.Changes[0].Breaking {
		t.Fatal("adding an object type is not breaking")
	}
	if diff.HasBreakingChanges {
		t.Fatal("diff must not report breaking changes")
	}
}

func TestDiffDetectsRemovedObjectTypeAsBreaking(t *testing.T) {
	before := validAviationSchema()
	after := validAviationSchema()
	after.ObjectTypes = after.ObjectTypes[:1]

	diff := DiffOntologySchemas(before, after)
	if !diff.HasBreakingChanges {
		t.Fatalf("removing an object type must be breaking, got %+v", diff.Changes)
	}
}

func TestDiffDetectsPrimaryKeyChangeAsBreaking(t *testing.T) {
	before := validAviationSchema()
	after := validAviationSchema()
	after.ObjectTypes[0].PrimaryKey = "airportName"
	after.ObjectTypes[0].Properties[1].Required = true

	diff := DiffOntologySchemas(before, after)
	if !diff.HasBreakingChanges {
		t.Fatal("changing a primary key re-identifies every object and must be breaking")
	}
}

func TestDiffDetectsPropertyTypeNarrowingAsBreaking(t *testing.T) {
	before := validAviationSchema()
	after := validAviationSchema()
	after.ObjectTypes[0].Properties[1].DataType = OntologyDataType{Kind: OntologyDataInteger}

	diff := DiffOntologySchemas(before, after)
	if !diff.HasBreakingChanges {
		t.Fatal("changing a property's data type must be breaking")
	}
}

func TestDiffDetectsNewRequiredPropertyAsBreaking(t *testing.T) {
	before := validAviationSchema()
	after := validAviationSchema()
	after.ObjectTypes[0].Properties = append(after.ObjectTypes[0].Properties, OntologyProperty{
		APIName: "runwayCount", DataType: OntologyDataType{Kind: OntologyDataInteger}, Required: true,
	})

	diff := DiffOntologySchemas(before, after)
	if !diff.HasBreakingChanges {
		t.Fatal("adding a required property invalidates existing objects and must be breaking")
	}
}

func TestDiffDetectsOptionalPropertyAdditionAsSafe(t *testing.T) {
	before := validAviationSchema()
	after := validAviationSchema()
	after.ObjectTypes[0].Properties = append(after.ObjectTypes[0].Properties, OntologyProperty{
		APIName: "runwayCount", DataType: OntologyDataType{Kind: OntologyDataInteger},
	})

	diff := DiffOntologySchemas(before, after)
	if diff.HasBreakingChanges {
		t.Fatalf("adding an optional property is safe, got %+v", diff.Changes)
	}
	if len(diff.Changes) != 1 {
		t.Fatalf("expected the addition to be reported, got %+v", diff.Changes)
	}
}

func TestDiffDetectsCardinalityTighteningAsBreaking(t *testing.T) {
	before := validAviationSchema()
	after := validAviationSchema()
	after.LinkTypes[0].A.Cardinality = OntologyCardinalityOne

	diff := DiffOntologySchemas(before, after)
	if !diff.HasBreakingChanges {
		t.Fatal("tightening MANY to ONE can invalidate existing edges and must be breaking")
	}
}

func TestDiffOnIdenticalSchemasIsEmpty(t *testing.T) {
	diff := DiffOntologySchemas(validAviationSchema(), validAviationSchema())
	if len(diff.Changes) != 0 {
		t.Fatalf("expected no changes, got %+v", diff.Changes)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./pkg/cortexdb -run TestDiff -v`
Expected: `undefined: DiffOntologySchemas`。

- [ ] **Step 3: 写实现**

```go
package cortexdb

import (
	"fmt"
	"sort"
)

// OntologyChange is one difference between two schema versions.
type OntologyChange struct {
	Kind     string `json:"kind"`
	Target   string `json:"target"`
	Detail   string `json:"detail"`
	Breaking bool   `json:"breaking"`
}

// OntologyDiff reports what changed between two schema versions, and whether
// any change invalidates data already in the graph.
type OntologyDiff struct {
	Changes            []OntologyChange `json:"changes"`
	HasBreakingChanges bool             `json:"has_breaking_changes"`
}

// DiffOntologySchemas compares two schema versions. "Breaking" means objects
// or edges already written under `before` would no longer validate under
// `after` — which is exactly the class of change that silently corrupts a
// graph if applied without warning.
func DiffOntologySchemas(before OntologySchema, after OntologySchema) OntologyDiff {
	diff := OntologyDiff{Changes: make([]OntologyChange, 0)}

	beforeObjects := indexObjectTypes(before)
	afterObjects := indexObjectTypes(after)

	for key, beforeType := range beforeObjects {
		afterType, ok := afterObjects[key]
		if !ok {
			diff.add("object_type_removed", beforeType.APIName,
				"object type removed; existing objects of this type no longer validate", true)
			continue
		}
		diff.diffObjectType(beforeType, afterType)
	}
	for key, afterType := range afterObjects {
		if _, ok := beforeObjects[key]; !ok {
			diff.add("object_type_added", afterType.APIName, "object type added", false)
		}
	}

	beforeLinks := indexLinkTypes(before)
	afterLinks := indexLinkTypes(after)

	for key, beforeLink := range beforeLinks {
		afterLink, ok := afterLinks[key]
		if !ok {
			diff.add("link_type_removed", beforeLink.APIName,
				"link type removed; existing edges of this type no longer validate", true)
			continue
		}
		diff.diffLinkType(beforeLink, afterLink)
	}
	for key, afterLink := range afterLinks {
		if _, ok := beforeLinks[key]; !ok {
			diff.add("link_type_added", afterLink.APIName, "link type added", false)
		}
	}

	sort.Slice(diff.Changes, func(i, j int) bool {
		if diff.Changes[i].Target != diff.Changes[j].Target {
			return diff.Changes[i].Target < diff.Changes[j].Target
		}
		return diff.Changes[i].Kind < diff.Changes[j].Kind
	})
	return diff
}

func (d *OntologyDiff) add(kind string, target string, detail string, breaking bool) {
	d.Changes = append(d.Changes, OntologyChange{Kind: kind, Target: target, Detail: detail, Breaking: breaking})
	if breaking {
		d.HasBreakingChanges = true
	}
}

func (d *OntologyDiff) diffObjectType(before OntologyObjectType, after OntologyObjectType) {
	if ontologyAPIKey(before.PrimaryKey) != ontologyAPIKey(after.PrimaryKey) {
		d.add("primary_key_changed", before.APIName,
			fmt.Sprintf("primary key %q -> %q re-identifies every existing object", before.PrimaryKey, after.PrimaryKey), true)
	}

	beforeProperties := indexProperties(before.Properties)
	afterProperties := indexProperties(after.Properties)

	for key, beforeProperty := range beforeProperties {
		afterProperty, ok := afterProperties[key]
		if !ok {
			d.add("property_removed", before.APIName+"."+beforeProperty.APIName, "property removed", true)
			continue
		}
		if beforeProperty.DataType.Kind != afterProperty.DataType.Kind {
			d.add("property_type_changed", before.APIName+"."+beforeProperty.APIName,
				fmt.Sprintf("data type %q -> %q; existing values may not parse", beforeProperty.DataType.Kind, afterProperty.DataType.Kind), true)
		}
		if !beforeProperty.Required && afterProperty.Required {
			d.add("property_became_required", before.APIName+"."+beforeProperty.APIName,
				"property became required; objects without it no longer validate", true)
		}
	}
	for key, afterProperty := range afterProperties {
		if _, ok := beforeProperties[key]; ok {
			continue
		}
		if afterProperty.Required {
			d.add("required_property_added", before.APIName+"."+afterProperty.APIName,
				"required property added; every existing object is missing it", true)
		} else {
			d.add("property_added", before.APIName+"."+afterProperty.APIName, "optional property added", false)
		}
	}
}

func (d *OntologyDiff) diffLinkType(before OntologyLinkType, after OntologyLinkType) {
	for _, pair := range []struct {
		label  string
		before OntologyLinkSide
		after  OntologyLinkSide
	}{
		{"a", before.A, after.A},
		{"b", before.B, after.B},
	} {
		if ontologyAPIKey(pair.before.ObjectTypeAPIName) != ontologyAPIKey(pair.after.ObjectTypeAPIName) {
			d.add("link_side_retargeted", before.APIName+"."+pair.label,
				fmt.Sprintf("side object type %q -> %q", pair.before.ObjectTypeAPIName, pair.after.ObjectTypeAPIName), true)
		}
		if pair.before.Cardinality == OntologyCardinalityMany && pair.after.Cardinality == OntologyCardinalityOne {
			d.add("cardinality_tightened", before.APIName+"."+pair.label,
				"cardinality MANY -> ONE; objects with several links no longer validate", true)
		}
		if pair.before.Cardinality == OntologyCardinalityOne && pair.after.Cardinality == OntologyCardinalityMany {
			d.add("cardinality_relaxed", before.APIName+"."+pair.label, "cardinality ONE -> MANY", false)
		}
	}
}

func indexObjectTypes(schema OntologySchema) map[string]OntologyObjectType {
	index := make(map[string]OntologyObjectType, len(schema.ObjectTypes))
	for _, objectType := range schema.ObjectTypes {
		index[ontologyAPIKey(objectType.APIName)] = objectType
	}
	return index
}

func indexLinkTypes(schema OntologySchema) map[string]OntologyLinkType {
	index := make(map[string]OntologyLinkType, len(schema.LinkTypes))
	for _, linkType := range schema.LinkTypes {
		index[ontologyAPIKey(linkType.APIName)] = linkType
	}
	return index
}

func indexProperties(properties []OntologyProperty) map[string]OntologyProperty {
	index := make(map[string]OntologyProperty, len(properties))
	for _, property := range properties {
		index[ontologyAPIKey(property.APIName)] = property
	}
	return index
}
```

工具面——在 `ontology_api.go` 追加：

```go
// OntologyDiffRequest compares a candidate schema against a stored one.
type OntologyDiffRequest struct {
	SchemaID  string         `json:"schema_id"`
	Candidate OntologySchema `json:"candidate"`
}

// OntologyDiffResponse reports the differences and whether any break data.
type OntologyDiffResponse struct {
	Diff OntologyDiff `json:"diff"`
}

func (db *DB) DiffOntologySchema(ctx context.Context, req OntologyDiffRequest) (*OntologyDiffResponse, error) {
	stored, err := db.loadOntologySchema(ctx, req.SchemaID)
	if err != nil {
		return nil, err
	}
	return &OntologyDiffResponse{Diff: DiffOntologySchemas(*stored, req.Candidate)}, nil
}

func (t *GraphRAGToolbox) DiffOntologySchema(ctx context.Context, req OntologyDiffRequest) (*OntologyDiffResponse, error) {
	return t.db.DiffOntologySchema(ctx, req)
}
```

工具定义、dispatch、MCP 注册照 Task 15 模式各加一段，工具名 `ontology_diff`。

- [ ] **Step 4: 跑测试确认通过**

Run: `go test -race ./pkg/cortexdb`
Expected: 全部 PASS。

- [ ] **Step 5: Commit**

```bash
git add pkg/cortexdb/ontology_diff.go pkg/cortexdb/ontology_api.go \
        pkg/cortexdb/ontology_tooldefs.go pkg/cortexdb/ontology_dispatch.go \
        pkg/cortexdb/ontology_mcp.go pkg/cortexdb/ontology_diff_test.go
git commit -m "feat(ontology): diff schema versions and flag breaking changes"
```

---

### Task 22: 端到端示例

**Files:**
- Create: `examples/12_ontology/main.go`
- Test: 由 CI 的 "examples compile" 步骤覆盖

- [ ] **Step 1: 确认现有 examples 编号**

Run: `ls examples/`
选一个未占用的编号；下面按 `12_ontology` 写，实际以 `ls` 结果为准。

- [ ] **Step 2: 写示例**

```go
// Command ontology demonstrates the Palantir-style ontology: typed object
// types with primary keys, per-side link cardinality, interface polymorphism,
// composable object sets, and governed writes through action types.
package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
)

func main() {
	dbPath := "ontology_example.db"
	defer func() { _ = os.Remove(dbPath) }()

	db, err := cortexdb.Open(cortexdb.DefaultConfig(dbPath))
	if err != nil {
		log.Fatalf("open: %v", err)
	}
	defer func() { _ = db.Close() }()

	ctx := context.Background()

	schema := cortexdb.OntologySchema{
		SchemaID: "aviation",
		Name:     "Aviation",
		InterfaceTypes: []cortexdb.OntologyInterfaceType{
			{
				APIName: "Facility",
				Properties: []cortexdb.OntologyProperty{
					{APIName: "facilityName", DataType: cortexdb.OntologyDataType{Kind: cortexdb.OntologyDataString}, Required: true},
				},
			},
		},
		ObjectTypes: []cortexdb.OntologyObjectType{
			{
				APIName:       "Airport",
				PrimaryKey:    "iataCode",
				TitleProperty: "facilityName",
				Implements:    []string{"Facility"},
				Properties: []cortexdb.OntologyProperty{
					{APIName: "iataCode", DataType: cortexdb.OntologyDataType{Kind: cortexdb.OntologyDataString}, Required: true},
					{APIName: "facilityName", DataType: cortexdb.OntologyDataType{Kind: cortexdb.OntologyDataString}, Required: true, Searchable: true},
					{APIName: "capacity", DataType: cortexdb.OntologyDataType{Kind: cortexdb.OntologyDataInteger}},
				},
			},
			{
				APIName:       "Flight",
				PrimaryKey:    "flightNumber",
				TitleProperty: "flightNumber",
				Properties: []cortexdb.OntologyProperty{
					{APIName: "flightNumber", DataType: cortexdb.OntologyDataType{Kind: cortexdb.OntologyDataString}, Required: true},
					{APIName: "originIata", DataType: cortexdb.OntologyDataType{Kind: cortexdb.OntologyDataString}},
				},
			},
		},
		LinkTypes: []cortexdb.OntologyLinkType{
			{
				APIName: "flightDeparture",
				A: cortexdb.OntologyLinkSide{
					APIName: "departures", ObjectTypeAPIName: "Airport",
					Cardinality: cortexdb.OntologyCardinalityMany,
				},
				B: cortexdb.OntologyLinkSide{
					APIName: "origin", ObjectTypeAPIName: "Flight",
					Cardinality: cortexdb.OntologyCardinalityOne, ForeignKeyProperty: "originIata",
				},
			},
		},
		ActionTypes: []cortexdb.OntologyActionType{
			{
				APIName: "registerAirport",
				Parameters: []cortexdb.OntologyActionParameter{
					{APIName: "iataCode", DataType: cortexdb.OntologyDataType{Kind: cortexdb.OntologyDataString}, Required: true},
					{APIName: "facilityName", DataType: cortexdb.OntologyDataType{Kind: cortexdb.OntologyDataString}, Required: true},
				},
				Rules: []cortexdb.OntologyActionRule{
					{
						Kind:       cortexdb.ActionRuleCreateObject,
						ObjectType: "Airport",
						PropertyValues: map[string]cortexdb.OntologyValueSource{
							"iataCode":     {Kind: cortexdb.ValueSourceParameter, Parameter: "iataCode"},
							"facilityName": {Kind: cortexdb.ValueSourceParameter, Parameter: "facilityName"},
						},
					},
				},
				SubmissionCriteria: []cortexdb.OntologySubmissionCriterion{
					{Parameter: "iataCode", Regex: "^[A-Z]{3}$", FailureMessage: "IATA code must be three uppercase letters."},
				},
			},
		},
	}

	if _, err := db.SaveOntologySchema(ctx, cortexdb.OntologySaveRequest{Schema: schema, Activate: true}); err != nil {
		log.Fatalf("save schema: %v", err)
	}
	fmt.Println("ontology activated")

	// Submission criteria reject a malformed code before anything is written.
	invalid, err := db.ApplyAction(ctx, cortexdb.ActionApplyRequest{
		Action:       "registerAirport",
		Parameters:   map[string]string{"iataCode": "heathrow", "facilityName": "London Heathrow"},
		ValidateOnly: true,
	})
	if err != nil {
		log.Fatalf("validate: %v", err)
	}
	fmt.Printf("validate-only on a bad code: valid=%v errors=%v\n", invalid.Valid, invalid.Errors)

	for _, airport := range [][2]string{{"LHR", "London Heathrow"}, {"LGW", "Gatwick"}} {
		resp, err := db.ApplyAction(ctx, cortexdb.ActionApplyRequest{
			Action:      "registerAirport",
			Parameters:  map[string]string{"iataCode": airport[0], "facilityName": airport[1]},
			ReturnEdits: true,
			Actor:       "example",
		})
		if err != nil {
			log.Fatalf("apply: %v", err)
		}
		fmt.Printf("registered %s, edits=%d\n", airport[0], len(resp.Edits))
	}

	// Interface polymorphism: query the abstract type, get every implementor.
	facilities, err := db.ResolveObjectSetObjects(ctx, cortexdb.ObjectSetResolveRequest{
		ObjectSet: cortexdb.ObjectSet{Kind: cortexdb.ObjectSetInterfaceBase, InterfaceType: "Facility"},
	})
	if err != nil {
		log.Fatalf("resolve: %v", err)
	}
	fmt.Printf("facilities via the interface: %d\n", facilities.Total)

	// Typed tools: the ontology becomes an agent-callable surface.
	tools, err := db.GenerateOntologyTools(ctx, cortexdb.OntologyToolGenOptions{IncludeObjectTypes: true})
	if err != nil {
		log.Fatalf("generate tools: %v", err)
	}
	fmt.Printf("generated %d typed tools\n", len(tools))
	for _, tool := range tools {
		fmt.Printf("  - %s\n", tool.Name)
	}
}
```

- [ ] **Step 3: 确认能编译并跑通**

Run: `(cd examples/12_ontology && go build -o /dev/null .) && go run ./examples/12_ontology`
Expected: 编译通过；输出依次为 ontology activated、validate-only valid=false、两条 registered、facilities 2、生成的工具清单。

- [ ] **Step 4: 跑全量测试**

Run: `go build ./... && go test -race ./...`
Expected: 全部 PASS。

- [ ] **Step 5: Commit**

```bash
git add examples/12_ontology/main.go
git commit -m "docs(examples): add an end-to-end ontology example"
```

---

### Task 23: 文档同步与版本发布

**Files:**
- Modify: `README.md`（新增 Ontology 章节）
- Modify: `README_CN.md`（对应中文章节）
- Modify: `SKILL.md:472`（工具清单更新）
- Modify: `version.go:4`
- Modify: `plugins/cortexdb/.claude-plugin/plugin.json:4`
- Modify: `plugins/cortexdb/.codex-plugin/plugin.json:3`
- Modify: `.claude-plugin/marketplace.json:12`

⚠️ CI 有版本一致性门禁（`.github/workflows/ci.yml:45-55`）：`version.go` 必须与**三份** manifest 完全一致，否则构建红。

- [ ] **Step 1: 写 README 章节**

在 `README.md` 的 Knowledge Graph 章节之后插入：

````markdown
## Ontology

CortexDB models a Palantir-style ontology: typed object types with a mandatory
primary key, link types with per-side cardinality, interfaces for polymorphic
retrieval, a composable object set algebra, and governed writes through action
types.

```go
_, err := db.SaveOntologySchema(ctx, cortexdb.OntologySaveRequest{
    Schema: cortexdb.OntologySchema{
        SchemaID: "aviation",
        ObjectTypes: []cortexdb.OntologyObjectType{{
            APIName:    "Airport",
            PrimaryKey: "iataCode",
            Implements: []string{"Facility"},
            Properties: []cortexdb.OntologyProperty{
                {APIName: "iataCode", DataType: cortexdb.OntologyDataType{Kind: cortexdb.OntologyDataString}, Required: true},
            },
        }},
    },
    Activate: true,
})
```

**Object types** carry `api_name`, `display_name`, `plural_display_name`,
`status`, `visibility`, `primary_key` (required), `title_property` and typed
`properties`. Supported data types: string, integer, long, double, decimal,
boolean, date, timestamp, geopoint, geoshape, vector, array, struct, marking.

**Link types** are bidirectional with two sides, each carrying its own
`api_name` and a `cardinality` of `ONE` or `MANY`. A one-to-many link is one
`ONE` side and one `MANY` side; the `ONE` side may name a
`foreign_key_property`.

**Interfaces** give polymorphism: an object set or `find_nodes` query against
`Facility` returns every implementing object type, and interfaces may extend
other interfaces.

**Object sets** compose retrieval:

```go
resolved, err := db.ResolveObjectSetObjects(ctx, cortexdb.ObjectSetResolveRequest{
    ObjectSet: cortexdb.ObjectSet{
        Kind:     cortexdb.ObjectSetIntersect,
        Operands: []cortexdb.ObjectSet{busyAirports, airportsNearLondon},
    },
})
```

Kinds: `base`, `interface_base`, `static`, `reference`, `filter`,
`search_around`, `union`, `intersect`, `subtract`. Filter predicates include
`eq`, `lt`, `lte`, `gt`, `gte`, `in`, `is_null`, `contains`, `starts_with`,
`contains_all_terms`, `contains_any_term`, `nearest_neighbors`, and the
boolean operators `and`, `or`, `not`. Vector KNN, full-text terms and link
traversal are peer operators in one expression. At most three chained
`search_around` hops, matching Foundry's limit.

**Action types** are governed writes: parameters, rules, submission criteria,
and an audit trail. Set `validate_only` to check without writing, or
`return_edits` to get the graph edits back (the two are mutually exclusive).
Setting `strict_actions: true` on the schema closes the generic upsert tools
so actions become the only write path.

**Not modelled**, deliberately: Foundry's function runtime, branches and
proposals, dynamic row-level security, and backing datasources. Those need a
platform CortexDB is not trying to be.
````

- [ ] **Step 2: 同步 README_CN.md 与 SKILL.md**

`README_CN.md` 写对应中文章节。`SKILL.md:472` 那行改为：

```markdown
- Ontology/inference: `ontology_save`, `ontology_get`, `ontology_list`, `ontology_delete`, `ontology_diff`, `ontology_action_list`, `ontology_action_apply`, `object_set_resolve`, `apply_inference`
```

- [ ] **Step 3: 同步版本号（四处必须一致）**

```bash
NEW_VERSION=2.67.0
sed -i '' "s/const Version = \".*\"/const Version = \"$NEW_VERSION\"/" version.go
sed -i '' "s/\"version\": \".*\"/\"version\": \"$NEW_VERSION\"/" plugins/cortexdb/.claude-plugin/plugin.json
sed -i '' "s/\"version\": \".*\"/\"version\": \"$NEW_VERSION\"/" plugins/cortexdb/.codex-plugin/plugin.json
sed -i '' "0,/\"version\": \".*\"/s//\"version\": \"$NEW_VERSION\"/" .claude-plugin/marketplace.json
```

`marketplace.json` 里可能有多个 `version` 键，`sed` 只改第一个未必对——改完务必用下一步的门禁脚本核对，必要时手工编辑。

- [ ] **Step 4: 跑 CI 的版本门禁与全量测试**

```bash
extract() { sed -n 's/.*"version"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' "$1" | head -n1; }
go_ver=$(sed -n 's/.*Version = "\([^"]*\)".*/\1/p' version.go)
echo "version.go=$go_ver"
echo "claude=$(extract plugins/cortexdb/.claude-plugin/plugin.json)"
echo "codex=$(extract plugins/cortexdb/.codex-plugin/plugin.json)"
echo "marketplace=$(extract .claude-plugin/marketplace.json)"

go build ./...
go test -race ./...
for dir in examples/*/; do (cd "$dir" && go build -o /dev/null .) || echo "FAILED: $dir"; done
```

Expected: 四个版本号完全一致；build、test、examples 全绿。

- [ ] **Step 5: Commit 第 6 期**

```bash
git add README.md README_CN.md SKILL.md version.go \
        plugins/cortexdb/.claude-plugin/plugin.json \
        plugins/cortexdb/.codex-plugin/plugin.json \
        .claude-plugin/marketplace.json
git commit -m "feat: Palantir-style ontology — typed objects, interfaces, object sets, actions (v2.67.0)"
```

---

## 完工检查

全部六期做完后跑一遍：

```bash
go build ./...
go test -race -cover ./...
go test ./pkg/cortexdb -run '^$' -bench 'BenchmarkRAG' -benchmem
for dir in examples/*/; do (cd "$dir" && go build -o /dev/null .) || echo "FAILED: $dir"; done
```

对照勾一遍：

- [ ] `ontology_schemas_v2` 建起来了，旧表 `graph_ontology_schemas` 从未被读写或删除
- [ ] 每个 ObjectType 都强制 `primary_key`，缺了就 schema 校验失败
- [ ] 节点身份 = `entity:<objectType>:<primaryKey>`；无 active schema 时回退旧的按 name 推 ID
- [ ] Link 每侧独立 `ONE`/`MANY`；`ONE` 侧才允许有外键属性
- [ ] 接口可多继承、可被多实现；环检测生效；按接口检索召回全部实现类型
- [ ] ObjectSet 九种 kind 全部可用；`search_around` 上限 3 层；向量/全文/图遍历同级可组合
- [ ] Action 的三条 Foundry 约束都落实了：link rule 仅多对多、不能改本次新建的对象、validate 不查图
- [ ] `validate_only` 与 `return_edits` 互斥；每次 apply 都写审计
- [ ] `strict_actions` 默认 false；打开后通用 upsert 关闭但 action 内部写入不受影响
- [ ] 生成的工具有数量上限，默认不注册到 MCP
- [ ] 所有新工具在 `Definitions()` 和 `NewMCPServer` 两处都注册了（`TestEveryToolDefinitionIsReachableOverMCP` 会抓）
- [ ] `version.go` 与三份 manifest 版本号一致
- [ ] README.md / README_CN.md / SKILL.md 三处文档同步

## 已知的实现期待定项

计划里有几处需要在动手时先核对现有代码，已在对应任务标注，集中列在这里：

1. `graph_edges` / `graph_nodes` 的实际表名与列名（Task 8 的 `countOntologyLinks`、Task 13 的 `resolveObjectSetByTypes`、Task 14 的 searchAround SQL）
2. `pkg/graph` 向量检索方法的真实签名（Task 15 的 `applyObjectSetVectorPredicate`）
3. `db.embedder` 字段名与 `Embed` 方法签名（同上）
4. `graph.GraphStore` 的 `UpsertNode` / `DeleteNode` / `GetNode` 签名（Task 18）
5. `ToolFindNodesRequest` 的类型过滤字段名（Task 11）
6. `toolObjectSchema` 里 `required` 的实际类型（Task 20 断言）
7. Task 14 searchAround 的 `nearSide`/`farSide` 方向——测试会立刻抓出反向，据此交换即可
8. Task 12 `ObjectSet` 自引用的 JSON 编解码：先试不带自定义 Marshal 的简单写法，通过就不要那层间接

---

# 补充任务（自查发现的缺口）

## Task 24: Shared properties 解析

> **插入位置：第 1 期，在 Task 4 之后、Task 5 之前。** 编号排在最后只是因为它是计划写完后自查补的；执行时按位置走。

自查发现 `OntologySchema.SharedProperties` 会被存储和序列化，但没有任何一处校验或使用它——是个死字段。Foundry 的 shared property 是「定义一次、跨多个对象类型复用、元数据集中管理」的属性。补上解析：对象类型的属性只写 `api_name` 而不写 `data_type` 时，从 shared properties 里取定义。

**Files:**
- Modify: `pkg/cortexdb/ontology_compile.go`（编译前展开 shared property）
- Modify: `pkg/cortexdb/ontology_validation.go`（校验前同样展开）
- Test: `pkg/cortexdb/ontology_shared_property_test.go`

- [ ] **Step 1: 写失败的测试**

```go
package cortexdb

import (
	"strings"
	"testing"
)

func sharedPropertySchema() OntologySchema {
	return OntologySchema{
		SchemaID: "shared",
		Name:     "Shared properties",
		SharedProperties: []OntologyProperty{
			{
				APIName:     "position",
				DisplayName: "Position",
				Description: "WGS84 location",
				DataType:    OntologyDataType{Kind: OntologyDataGeoPoint},
			},
		},
		ObjectTypes: []OntologyObjectType{
			{
				APIName:    "Airport",
				PrimaryKey: "iataCode",
				Properties: []OntologyProperty{
					{APIName: "iataCode", DataType: OntologyDataType{Kind: OntologyDataString}, Required: true},
					// No data_type: resolved from the shared property.
					{APIName: "position", Required: true},
				},
			},
			{
				APIName:    "Plant",
				PrimaryKey: "plantCode",
				Properties: []OntologyProperty{
					{APIName: "plantCode", DataType: OntologyDataType{Kind: OntologyDataString}, Required: true},
					{APIName: "position"},
				},
			},
		},
	}
}

func TestSharedPropertyIsResolvedIntoObjectTypes(t *testing.T) {
	if err := validateOntologySchema(sharedPropertySchema()); err != nil {
		t.Fatalf("expected the shared property to resolve, got %v", err)
	}

	compiled := compileOntology(sharedPropertySchema())
	property, ok := compiled.property("Airport", "position")
	if !ok {
		t.Fatal("expected position to resolve on Airport")
	}
	if property.DataType.Kind != OntologyDataGeoPoint {
		t.Fatalf("expected the shared data type, got %q", property.DataType.Kind)
	}
	if property.Description != "WGS84 location" {
		t.Fatalf("expected shared metadata to carry over, got %q", property.Description)
	}
}

func TestSharedPropertyKeepsPerObjectTypeRequiredFlag(t *testing.T) {
	compiled := compileOntology(sharedPropertySchema())

	airportPosition, _ := compiled.property("Airport", "position")
	if !airportPosition.Required {
		t.Fatal("Airport declares position required; the local flag must win")
	}
	plantPosition, _ := compiled.property("Plant", "position")
	if plantPosition.Required {
		t.Fatal("Plant leaves position optional; the local flag must win")
	}
}

func TestLocalDataTypeOverridesSharedProperty(t *testing.T) {
	schema := sharedPropertySchema()
	schema.ObjectTypes[0].Properties[1].DataType = OntologyDataType{Kind: OntologyDataString}

	compiled := compileOntology(schema)
	property, _ := compiled.property("Airport", "position")
	if property.DataType.Kind != OntologyDataString {
		t.Fatalf("an explicit local data type must win, got %q", property.DataType.Kind)
	}
}

func TestUnresolvedPropertyWithoutDataTypeIsRejected(t *testing.T) {
	schema := sharedPropertySchema()
	schema.SharedProperties = nil

	err := validateOntologySchema(schema)
	if err == nil || !strings.Contains(err.Error(), "position") {
		t.Fatalf("a property with no data type and no shared definition must be rejected, got %v", err)
	}
}

func TestSharedPropertyItselfIsValidated(t *testing.T) {
	schema := sharedPropertySchema()
	schema.SharedProperties[0].DataType = OntologyDataType{Kind: OntologyDataVector} // no dimension

	if err := validateOntologySchema(schema); err == nil {
		t.Fatal("an invalid shared property must be rejected")
	}
}

func TestDuplicateSharedPropertiesRejected(t *testing.T) {
	schema := sharedPropertySchema()
	schema.SharedProperties = append(schema.SharedProperties, schema.SharedProperties[0])

	if err := validateOntologySchema(schema); err == nil {
		t.Fatal("duplicate shared property api names must be rejected")
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./pkg/cortexdb -run 'TestSharedProperty|TestLocalDataTypeOverrides|TestUnresolvedProperty|TestDuplicateShared' -v`
Expected: `expected the shared property to resolve` —— 校验会因 `position` 的 `data_type.kind` 为空而报 unknown data type kind。

- [ ] **Step 3: 写实现**

在 `pkg/cortexdb/ontology_compile.go` 顶部加解析函数，并让 `compileOntology` 先解析再建表：

```go
// resolveSharedProperties expands object type properties that reference a
// shared property by name alone. A shared property is defined once and reused
// across object types; the local declaration keeps its own Required flag and
// may override any field it states explicitly.
func resolveSharedProperties(schema OntologySchema) OntologySchema {
	if len(schema.SharedProperties) == 0 {
		return schema
	}
	shared := make(map[string]OntologyProperty, len(schema.SharedProperties))
	for _, property := range schema.SharedProperties {
		shared[ontologyAPIKey(property.APIName)] = property
	}

	objectTypes := make([]OntologyObjectType, 0, len(schema.ObjectTypes))
	for _, objectType := range schema.ObjectTypes {
		properties := make([]OntologyProperty, 0, len(objectType.Properties))
		for _, property := range objectType.Properties {
			properties = append(properties, mergeSharedProperty(shared, property))
		}
		objectType.Properties = properties
		objectTypes = append(objectTypes, objectType)
	}
	schema.ObjectTypes = objectTypes

	interfaceTypes := make([]OntologyInterfaceType, 0, len(schema.InterfaceTypes))
	for _, interfaceType := range schema.InterfaceTypes {
		properties := make([]OntologyProperty, 0, len(interfaceType.Properties))
		for _, property := range interfaceType.Properties {
			properties = append(properties, mergeSharedProperty(shared, property))
		}
		interfaceType.Properties = properties
		interfaceTypes = append(interfaceTypes, interfaceType)
	}
	schema.InterfaceTypes = interfaceTypes
	return schema
}

func mergeSharedProperty(shared map[string]OntologyProperty, local OntologyProperty) OntologyProperty {
	definition, ok := shared[ontologyAPIKey(local.APIName)]
	if !ok {
		return local
	}
	// Only fill in what the local declaration left blank. Required is
	// deliberately never inherited: whether a property is mandatory is a
	// per-object-type decision, not a property of the shared definition.
	if local.DataType.Kind == "" {
		local.DataType = definition.DataType
	}
	if local.DisplayName == "" {
		local.DisplayName = definition.DisplayName
	}
	if local.Description == "" {
		local.Description = definition.Description
	}
	if !local.Searchable {
		local.Searchable = definition.Searchable
	}
	if !local.Vectorized {
		local.Vectorized = definition.Vectorized
	}
	return local
}
```

`compileOntology` 第一行改为：

```go
func compileOntology(schema OntologySchema) *compiledOntology {
	schema = resolveSharedProperties(schema)
	compiled := &compiledOntology{
		schema: schema,
		// ...unchanged
```

`validateOntologySchema` 第一件事也改成先解析，并加上 shared property 自身的校验：

```go
func validateOntologySchema(schema OntologySchema) error {
	if strings.TrimSpace(schema.SchemaID) == "" {
		return fmt.Errorf("schema_id is required")
	}

	sharedSeen := make(map[string]struct{}, len(schema.SharedProperties))
	for _, property := range schema.SharedProperties {
		if err := validateOntologyProperty("shared", property); err != nil {
			return err
		}
		key := ontologyAPIKey(property.APIName)
		if _, exists := sharedSeen[key]; exists {
			return fmt.Errorf("duplicate shared property %q", property.APIName)
		}
		sharedSeen[key] = struct{}{}
	}

	schema = resolveSharedProperties(schema)

	// ...rest unchanged
```

存储层不变——`saveOntologySchemaRecord` 存的是调用方给的原始 schema（未展开的），这是对的：shared property 的意义就在于集中管理，展开后存会让后续改 shared 定义无法生效。

- [ ] **Step 4: 跑测试确认通过**

Run: `go test -race ./pkg/cortexdb`
Expected: 本任务 6 个测试 PASS，第 1/3/5 期已有测试仍 PASS。

- [ ] **Step 5: Commit**

```bash
git add pkg/cortexdb/ontology_compile.go pkg/cortexdb/ontology_validation.go \
        pkg/cortexdb/ontology_shared_property_test.go
git commit -m "feat(ontology): resolve shared properties into object and interface types"
```

> 连带影响：Task 21 的 `DiffOntologySchemas` 拿到的是**未展开**的 schema，所以改 shared property 的 data type 不会被识别为破坏性变更。执行 Task 21 时在 `DiffOntologySchemas` 开头对两个入参各调一次 `resolveSharedProperties`，并补一个测试：改 shared property 的 data type 必须报 breaking。
