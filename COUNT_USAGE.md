# Count 功能测试和使用指南

## 功能说明

新增的 `count` 命令用于并发计算数据库表中的 KEY 个数，支持以下特性：

1. **并发计数**：使用多线程并发扫描，提高大数据集的统计性能
2. **数据库原生支持**：对于实现了 `CountDB` 接口的数据库（MySQL/TiDB），使用原生的 COUNT 查询
3. **通用扫描模式**：对于未实现 `CountDB` 接口的数据库，使用批量扫描方式统计
4. **实时进度显示**：每 2 秒更新一次已统计的 KEY 数量

## 已实现的数据库

### 原生 Count 支持
- **MySQL/TiDB/MariaDB**: 使用 `SELECT COUNT(*) FROM table` 查询
- **TiKV (raw模式)**: 使用 TiKV 的 Scan API 进行范围扫描统计
- **TiKV (txn模式)**: 使用事务迭代器进行统计

### 通用扫描模式
其他所有数据库会使用通用的扫描模式进行统计。

## 命令使用

### 基本语法

```bash
./bin/go-ycsb count <数据库名> [选项]
```

### 选项参数

| 参数 | 默认值 | 说明 |
|------|--------|------|
| `-P, --property_file` | - | 指定属性文件路径 |
| `-p, --prop` | - | 指定单个属性 (格式: name=value) |
| `--table` | usertable | 指定表名 |
| `--threads` | 10 | 并发线程数 |
| `--batch` | 1000 | 批量扫描大小（仅用于扫描模式） |

## 使用示例

### 1. MySQL/TiDB 统计

```bash
# 统计 MySQL 数据库中的 KEY 数量
./bin/go-ycsb count mysql \
  -p mysql.host=127.0.0.1 \
  -p mysql.port=3306 \
  -p mysql.user=root \
  -p mysql.password=password \
  -p mysql.db=test \
  --table usertable

# 使用配置文件
./bin/go-ycsb count tidb -P workloads/workloada
```

**输出示例：**
```
***************** properties *****************
"mysql.host"="127.0.0.1"
"mysql.port"="3306"
"table"="usertable"
**********************************************
Using native count implementation for mysql
Counting keys in table 'usertable'...
**********************************************
Total keys: 1000000
Time elapsed: 1.234s
Count rate: 810372.15 keys/sec
**********************************************
```

### 2. TiKV 统计

```bash
# Raw 模式
./bin/go-ycsb count tikv \
  -p tikv.pd=127.0.0.1:2379 \
  -p tikv.type=raw \
  --table usertable

# Txn 模式
./bin/go-ycsb count tikv \
  -p tikv.pd=127.0.0.1:2379 \
  -p tikv.type=txn \
  --table usertable
```

### 3. 使用并发统计（扫描模式）

```bash
# 使用 20 个并发线程
./bin/go-ycsb count redis \
  -p redis.addr=127.0.0.1:6379 \
  --table usertable \
  --threads 20 \
  --batch 5000
```

**输出示例（带进度）：**
```
***************** properties *****************
"redis.addr"="127.0.0.1:6379"
"table"="usertable"
"recordcount"="10000000"
**********************************************
Using scan-based concurrent count for redis
Counting keys in table 'usertable' using 20 threads...
Estimated record count: 10000000
Progress: 5234567 keys counted...
**********************************************
Total keys counted: 10000000
Time elapsed: 45.67s
Count rate: 219019.52 keys/sec
Threads used: 20
**********************************************
```

## 性能优化建议

### MySQL/TiDB
- 原生 COUNT 查询通常最快
- 如果表很大，可能需要较长时间
- 考虑添加适当的索引

### TiKV
- 调整 `tikv.conncount` 和 `tikv.batchsize` 参数
- Raw 模式通常比 Txn 模式更快（用于统计）
- 大数据集建议使用 Raw 模式

### 通用扫描模式
- 增加 `--threads` 参数提高并发度（建议 10-50）
- 调整 `--batch` 大小（建议 1000-10000）
- 确保 `recordcount` 属性正确设置

## 实现细节

### 接口定义

```go
// pkg/ycsb/db.go
type CountDB interface {
    Count(ctx context.Context, table string) (int64, error)
}
```

### 为新数据库实现 Count

如果要为其他数据库添加原生 Count 支持：

```go
// db/yourdb/db.go
func (db *yourDB) Count(ctx context.Context, table string) (int64, error) {
    // 实现数据库特定的统计逻辑
    // 例如：执行 COUNT 查询或使用 API 统计
    return count, nil
}
```

## 测试场景

### 1. 数据加载后验证

```bash
# 加载数据
./bin/go-ycsb load mysql -P workloads/workloada

# 验证加载的数据量
./bin/go-ycsb count mysql -P workloads/workloada
```

### 2. 性能测试后检查

```bash
# 运行性能测试
./bin/go-ycsb run tikv -P workloads/workloada

# 检查数据是否完整
./bin/go-ycsb count tikv -P workloads/workloada
```

### 3. 多表统计

```bash
# 统计不同的表
./bin/go-ycsb count mysql -p mysql.db=test --table users
./bin/go-ycsb count mysql -p mysql.db=test --table orders
./bin/go-ycsb count mysql -p mysql.db=test --table products
```

## 故障排除

### 问题：统计结果为 0
- 检查表名是否正确
- 检查数据库连接是否正常
- 对于扫描模式，检查 `recordcount` 配置

### 问题：统计速度慢
- 增加并发线程数（`--threads`）
- 调整批量大小（`--batch`）
- 对于 MySQL，考虑使用索引优化

### 问题：内存占用高
- 减少并发线程数
- 减少批量大小
- 对于大数据集，考虑分段统计

## 与其他命令的配合

```bash
# 完整的测试流程
# 1. 加载数据
./bin/go-ycsb load tidb -P workloads/workloada -p recordcount=1000000

# 2. 验证数据量
./bin/go-ycsb count tidb -P workloads/workloada
# 预期输出: Total keys: 1000000

# 3. 运行性能测试
./bin/go-ycsb run tidb -P workloads/workloada

# 4. 再次验证数据完整性
./bin/go-ycsb count tidb -P workloads/workloada
```

## 注意事项

1. **扫描模式依赖 recordcount**：通用扫描模式需要正确的 `recordcount` 配置
2. **并发数选择**：过多的并发可能给数据库带来压力
3. **精确度**：扫描模式在某些情况下可能不完全精确（如数据正在变化）
4. **权限要求**：确保数据库用户有相应的查询权限
