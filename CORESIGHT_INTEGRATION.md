# CoreDog × CoreSight 集成指南

本文档说明如何配置 CoreDog agent 与 CoreSight 集成，自动上报 coredump 已上传事件。

## 概述

CoreDog 作为 Kubernetes 集群中的 core dump 收集系统，在成功上传 core dump 文件后，可以向 CoreSight API 上报 `coredog.coredump.uploaded` 事件，从而实现以下功能：

- ✅ 自动触发 CoreSight 分析任务
- ✅ 统一管理所有 core dump 分析
- ✅ 完整的分析链路可视化

## 配置步骤

### 1. 获取 CoreSight Agent Token

首先需要在 CoreSight 系统中注册 CoreDog 作为一个 agent，获取 token。

```bash
# 运行 CoreSight 初始化脚本
cd /path/to/coresight
bash scripts/init_auth.sh

# 输出示例：
# Agent Token: kULT1AKXMu4xxnPNo5fSCW7J5LXx7llJgLh45PCuxOg
```

保存好这个 token，稍后会用到。

### 2. 配置 CoreDog Helm 值

编辑 `coredog/charts/values.yaml`，添加或修改 CoreSight 配置部分：

```yaml
config:
  coredog: |-
    # ... 其他现有配置 ...
    
    # CoreSight 集成配置（新增）
    CoreSight:
      enabled: true
      apiUrl: "http://coresight-api.default.svc.cluster.local:8000"  # CoreSight API 地址
      token: "kULT1AKXMu4xxnPNo5fSCW7J5LXx7llJgLh45PCuxOg"           # Agent token
```

**配置说明**：

| 字段 | 说明 | 示例 |
|------|------|------|
| `enabled` | 是否启用 CoreSight 集成 | `true` / `false` |
| `apiUrl` | CoreSight API 地址（集群内可用 DNS）| `http://coresight-api:8000` |
| `token` | CoreSight agent token（从 init_auth.sh 获取）| `kULT1AKXMu...` |

### 3. 安装或升级 CoreDog

**首次安装**：
```bash
helm install coredog ./charts -n coredog-system --create-namespace -f charts/values.yaml
```

**升级现有部署**：
```bash
helm upgrade coredog ./charts -n coredog-system -f charts/values.yaml
```

### 4. 验证集成

检查 CoreDog agent 日志中是否有 CoreSight 集成信息：

```bash
# 查看 watcher 日志
kubectl logs -n coredog-system -l app.kubernetes.io/component=watcher -f

# 应该看到类似输出：
# time="2025-12-12T15:30:00Z" level=info msg="CoreSight integration enabled: http://coresight-api:8000"
# time="2025-12-12T15:30:05Z" level=info msg="[CoreSight] Successfully reported coredump uploaded event: https://cos.xxx/core.xxx (event_id: coredog-1765567205-12345)"
```

## 环境变量配置（可选）

除了在 ConfigMap 中配置，也可以通过环境变量覆盖配置：

```bash
# 设置环境变量
export CORESIGHT_API_URL="http://coresight-api:8000"
export CORESIGHT_AGENT_TOKEN="your-token-here"
```

在 Kubernetes 部署中，添加环境变量到 watcher 容器：

```yaml
env:
- name: CORESIGHT_API_URL
  value: "http://coresight-api.default.svc.cluster.local:8000"
- name: CORESIGHT_AGENT_TOKEN
  valueFrom:
    secretKeyRef:
      name: coresight-credentials
      key: agent-token
```

## 工作流程

当 CoreDog 检测到并成功上传 core dump 后，以下流程会自动触发：

```
┌─────────────────┐
│  应用崩溃       │
│ 生成 core dump  │
└────────┬────────┘
         │
         ▼
┌─────────────────────────────┐
│ CoreDog 检测文件            │
│ • 文件监控                  │
│ • 获取 Pod 信息             │
└────────┬────────────────────┘
         │
         ▼
┌─────────────────────────────┐
│ 上传到 S3                   │
│ • 生成下载 URL              │
│ • 清理本地文件              │
└────────┬────────────────────┘
         │
         ▼
┌─────────────────────────────┐
│ 发送通知                    │
│ • 企业微信                  │
│ • Slack                     │
└────────┬────────────────────┘
         │
         ▼
┌─────────────────────────────┐
│ 上报事件到 CoreSight ◄──────┤ (新增)
│ CloudEvent 格式             │
│ • 事件类型                  │
│ • 文件 URL                  │
│ • Pod 信息                  │
└────────┬────────────────────┘
         │
         ▼
┌─────────────────────────────┐
│ CoreSight 创建分析任务      │
│ • 自动创建 AnalysisJob      │
│ • 提交到分析队列            │
│ • 执行 GDB 分析             │
└─────────────────────────────┘
```

## 上报事件格式

CoreDog 向 CoreSight 上报的 CloudEvents 格式如下：

```json
{
  "specversion": "1.0",
  "type": "coredog.coredump.uploaded",
  "source": "coredog-agent",
  "id": "coredog-1765567205-12345",
  "time": "2025-12-12T15:30:05Z",
  "datacontenttype": "application/json",
  "data": {
    "coredump_id": 0,
    "file_url": "https://cos.ap-nanjing.myqcloud.com/dumps/core.bash.123456",
    "executable_path": "example-pod-abc123",
    "file_size": 52428800,
    "md5": "",
    "image": "ubuntu:22.04",
    "timestamp": "2025-12-12T15:30:05Z",
    "pod_name": "example-pod-abc123",
    "pod_namespace": "default",
    "node_name": "node-1"
  }
}
```

**数据字段说明**：

| 字段 | 说明 | 示例 |
|------|------|------|
| `coredump_id` | Core dump ID（由 CoreSight 自动分配） | `0` |
| `file_url` | 上传到 S3 的 core dump URL | `https://cos.xxx/core.xxx` |
| `executable_path` | 可执行文件路径 | `example-pod-abc123` |
| `file_size` | 文件大小（字节） | `52428800` |
| `image` | 容器镜像 | `ubuntu:22.04` |
| `timestamp` | 上报时间 | `2025-12-12T15:30:05Z` |
| `pod_name` | Pod 名称 | `example-pod-abc123` |
| `pod_namespace` | Pod 命名空间 | `default` |
| `node_name` | Node 名称 | `node-1` |

## 故障排查

### CoreSight 集成未启用

**问题**：日志中没有看到 CoreSight 相关信息

**排查**：
```bash
# 检查配置
kubectl get cm -n coredog-system coredog -o yaml | grep -A 3 CoreSight

# 确保以下配置存在：
# CoreSight:
#   enabled: true
#   apiUrl: "http://coresight-api:8000"
#   token: "your-token"
```

### 事件上报失败

**问题**：日志显示 "failed to report coredump to CoreSight"

**排查**：
```bash
# 1. 检查 CoreSight API 可访问性
kubectl run -it --rm debug --image=curlimages/curl --restart=Never -- \
  curl -v http://coresight-api:8000/api/v1/health

# 2. 检查网络连接（从 coredog pod）
kubectl exec -it <coredog-pod> -n coredog-system -- \
  curl -v http://coresight-api:8000/api/v1/health

# 3. 检查 token 是否有效
kubectl get secret -n coredog-system coresight-token -o yaml
```

### CoreSight 中未收到事件

**问题**：CoreSight 中没有看到分析任务

**排查**：
```bash
# 1. 确认事件是否被 worker 接收
tail -f /path/to/worker.log | grep "Received coredump.uploaded"

# 2. 确认 NATS 连接正常
kubectl logs -n coredog-system -l app.kubernetes.io/component=watcher | grep -i nats
```

## 禁用集成

如果需要禁用 CoreSight 集成，只需将配置改为 `false`：

```yaml
CoreSight:
  enabled: false
```

或删除环境变量，然后升级部署：

```bash
helm upgrade coredog ./charts -n coredog-system
```

## 常见问题

**Q：CoreSight 和 CoreDog 需要在同一个集群吗？**

A：不需要。CoreDog 只需要能够通过网络访问 CoreSight API 即可。可以在不同集群或完全不同的环境中。

**Q：Token 泄露了怎么办？**

A：在 CoreSight 中重新生成 token，然后更新 CoreDog 配置并重新部署。

**Q：如果 CoreSight API 不可用会怎样？**

A：CoreDog 会记录错误日志但继续工作。不会影响 core dump 的收集和 S3 上传。事件上报是最后一步，失败不会中断前面的流程。

**Q：上报的 coredump_id 为什么是 0？**

A：CoreSight API 会自动分配，coredog 不需要预先知道。Core dump 会与该 ID 关联。

## 支持

如有问题，请检查：

1. CoreDog 日志：`kubectl logs -n coredog-system -l app.kubernetes.io/component=watcher`
2. CoreSight 日志：`tail -f /path/to/worker.log`
3. 网络连接：`kubectl exec <pod> -- curl http://coresight-api:8000/api/v1/health`
