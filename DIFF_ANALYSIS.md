# 本地版本与 GitHub 版本差异分析

## 概述

本地代码库基于 GitHub 官方仓库 `pingcap/go-ycsb`，当前位于 master 分支，与远程 origin/master 保持同步（最新提交：99b1dc5 "Merge pull request #307 from tigrisdata/master"）。

但有 **4 个文件被修改**，且有一些未跟踪的文件。

---

## 修改的文件

### 1. **Makefile** （注释了 SQLite 检测）

**修改内容：**
```diff
-SQLITE_CHECK := $(shell echo "int main() { return 0; }" | gcc -lsqlite3 -x c++ -o /dev/null - 2>/dev/null; echo $$?)
+#SQLITE_CHECK := $(shell echo "int main() { return 0; }" | gcc -lsqlite3 -x c++ -o /dev/null - 2>/dev/null; echo $$?)
```

**影响：**
- SQLite 库检测被禁用
- 构建时不会自动检测并启用 SQLite 支持
- 即使系统安装了 libsqlite3，也不会在编译时包含 SQLite 数据库支持

**可能原因：**
- 本地环境可能没有安装 SQLite 开发库
- 或者在构建时遇到了 SQLite 相关的编译问题
- 临时禁用以加快构建或避免错误

---

### 2. **go.mod** （依赖版本升级）

**主要变更：**

#### 直接依赖升级：
- `github.com/pingcap/kvproto`: `0220705` → `20230403` (升级约 9 个月)
- `github.com/tikv/client-go/v2`: `v2.0.1-0220720` → `v2.0.7` (major 版本升级)

#### 间接依赖升级（共 18 个）：
- `benbjohnson/clock`: v1.1.0 → v1.3.0
- `pingcap/failpoint`: 升级
- `pingcap/log`: 升级
- `prometheus/client_golang`: v1.11.1 → v1.14.0
- `prometheus/common`: v0.26.0 → v0.39.0
- `prometheus/procfs`: v0.6.0 → v0.9.0
- `uber.org/atomic`: v1.9.0 → v1.10.0
- `uber.org/multierr`: v1.7.0 → v1.9.0
- `uber.org/zap`: v1.20.0 → v1.24.0
- `natefinch/lumberjack.v2`: v2.0.0 → v2.2.1
- 以及其他多个小版本升级

**新增依赖：**
- `github.com/elastic/gosigar v0.14.2`
- `github.com/tiancaiamao/gp v0.0.0-20221230034425-4025bc8a4d4a`

**影响：**
- **TiKV 客户端库重大升级**：可能包含新特性、bug 修复或性能改进
- **监控相关库升级**：Prometheus 相关库大幅升级，可能改进指标收集
- **日志库升级**：zap 和 lumberjack 升级，可能有性能和功能改进
- **潜在兼容性问题**：依赖大幅升级可能引入不兼容变更

---

### 3. **go.sum** （+34 行依赖校验和）

**变更：**
```
新增 34 行校验和记录
```

**影响：**
- 对应 go.mod 中的依赖升级
- 确保依赖包的完整性和一致性
- 所有新版本依赖都有对应的校验和验证

---

### 4. **workloads/workloada** （工作负载配置修改）

**修改内容：**
```diff
-readproportion=0.5
-updateproportion=0.5
+readproportion=0
+updateproportion=1
```

**影响分析：**

**原始配置（GitHub）：**
- 50% 读操作
- 50% 更新操作
- 典型的读写混合负载

**修改后配置（本地）：**
- 0% 读操作
- 100% 更新操作
- **纯写入负载**

**意义：**
- 这是一个 **测试场景定制**，用于专门测试数据库的写入性能
- 可能用于以下场景：
  - 写入性能基准测试
  - 更新操作的性能分析
  - 写入瓶颈诊断
  - TiDB/TiKV 的写入性能评估

**注意：**
- 这个修改使 workloada 不再符合 YCSB 标准定义
- YCSB 标准的 Workload A 应该是 50/50 的读写混合
- 运行标准对比测试时应该恢复原始配置

---

## 未跟踪的文件

### 1. **CLAUDE.md** （新增）
- 刚刚创建的 Claude Code 指导文档
- 包含项目架构、构建命令、开发指南

### 2. **go.sum.bak** （备份文件）
- go.sum 的备份
- 可能是在依赖升级前的备份
- 时间戳：12月 16日（比当前 go.sum 早）

### 3. **nohup.out** （运行日志）
- 后台运行的输出日志（75KB）
- 包含 TiUP Playground 启动日志
- 运行了本地 TiDB 集群测试环境（v8.5.3）
  - 3 个 PD 实例
  - 3 个 TiKV 实例
  - 1 个 TiDB 实例（127.0.0.1:4000）

---

## 编译产物

### **bin/go-ycsb** （94MB）
- 已编译的二进制文件
- 时间戳：2月 3日
- 包含所有启用的数据库驱动

---

## 依赖管理

### **vendor/** 目录（75MB）
- 包含所有 Go 依赖的本地副本
- 支持离线构建
- 使用 `go mod vendor` 生成

---

## 总结

### 关键差异点：

1. **功能禁用**：SQLite 支持被临时禁用
2. **依赖升级**：TiKV 客户端和监控库大幅升级（约 9 个月的更新）
3. **测试定制**：Workload A 被修改为纯写入负载（100% update）
4. **本地测试环境**：运行了 TiDB v8.5.3 本地集群

### 与官方仓库的兼容性：

- ✅ **代码兼容**：所有代码文件与官方完全一致
- ⚠️ **依赖不同**：使用了更新的依赖版本
- ⚠️ **配置不同**：workloada 被定制化修改
- ⚠️ **构建配置不同**：SQLite 被禁用

### 建议：

1. **如果要贡献代码回官方**：
   - 恢复 Makefile 的 SQLite 检测
   - 恢复 workloads/workloada 到原始配置
   - 考虑是否将依赖升级作为单独的 PR 提交

2. **如果用于生产测试**：
   - 充分测试依赖升级后的稳定性
   - 记录 workloada 的定制原因
   - 考虑创建新的 workload 文件而不是修改标准文件

3. **清理建议**：
   - `.gitignore` 应包含：`nohup.out`, `*.bak`, `bin/`, `CLAUDE.md`（可选）
   - 考虑删除 `go.sum.bak` 备份文件
