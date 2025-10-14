# CoreDog 故障排查指南

## 问题 1: Pod 未被注入 volume

### 症状

```bash
kubectl get pod my-pod -o yaml | grep coredog
# 没有输出
```

### 排查

```bash
# 1. 检查 annotations
kubectl get pod my-pod -o yaml | grep -A 5 annotations

# 2. 查看 webhook 日志（会说明跳过原因）
kubectl logs -n coredog-system -l app.kubernetes.io/component=webhook | grep "my-pod"

# 3. 检查 webhook 状态
kubectl get pods -n coredog-system -l app.kubernetes.io/component=webhook
```

### 常见原因

| 日志信息 | 原因 | 解决方案 |
|---------|------|---------|
| `no annotations` | Pod 没有 annotations | 添加 annotations |
| `annotation coredog.io/inject not found` | 缺少 inject 注解 | 添加 `coredog.io/inject: "true"` |
| `inject=false` | 显式禁用了 | 改为 `"true"` |
| `coredog.io/path is required but not set` | 缺少 path 注解 | 添加 `coredog.io/path: "/corefile"` |
| `path=/etc is not allowed` | 使用了危险路径 | 改用安全路径如 `/corefile` |

## 问题 2: Core Dump 未被检测

### 症状

应用崩溃了，但 watcher 没有日志输出。

### 排查

```bash
# 1. 检查节点 core_pattern
ssh <node>
cat /proc/sys/kernel/core_pattern
# 应该是: /corefile/core.%e.%p.%h.%t
# 注意：必须与容器内的挂载路径一致（即 coredog.io/path 的值）

# 2. 检查 ulimit
kubectl exec <pod> -c <container> -- bash -c "ulimit -c"
# 应该是: unlimited

# 3. 检查文件是否生成
ssh <node>
ls -la /data/coredog-system/dumps/

# 4. 查看 watcher 日志
kubectl logs -n coredog-system -l app.kubernetes.io/component=watcher -f
```

### 解决方案

- 未设置 core_pattern → 执行节点配置命令
- ulimit 不是 unlimited → 在容器启动脚本中添加 `ulimit -c unlimited`
- 文件未生成 → 检查应用是否真的崩溃（SIGSEGV, SIGABRT 等信号）

## 问题 3: Pod 信息识别失败

### 症状

通知显示 `[/] core: xxx` 而不是 `[namespace/podname]`

### 排查

```bash
# 查看 watcher 日志
kubectl logs -n coredog-system -l app.kubernetes.io/component=watcher | grep "resolving pod info"

# 检查文件路径
ssh <node>
find /data/coredog-system/dumps -type f -ls
```

### 原因

路径格式不正确。正确格式：
```
/data/coredog-system/dumps/<namespace>/<podname>/core.xxx
```

### 解决

确认：
1. Pod 有 `coredog.io/inject: "true"` 和 `coredog.io/path` annotations
2. Webhook 正常运行
3. Pod 重新创建后才会有正确的 volume 路径

## 问题 4: 本地文件未清理

### 症状

节点磁盘不断增长。

### 排查

```bash
# 1. 检查配置
kubectl get cm -n coredog-system coredog -o yaml | grep DeleteLocalCorefile
# 应该是: true

# 2. 检查上传日志
kubectl logs -n coredog-system -l app.kubernetes.io/component=watcher | grep -E "(upload|deleted)"

# 3. 检查是否有错误
kubectl logs -n coredog-system -l app.kubernetes.io/component=watcher | grep error
```

### 原因

- `DeleteLocalCorefile: false` → 改为 `true`
- S3 上传失败 → 检查 S3 配置和网络
- 上传成功但删除失败 → 检查权限和日志

## 问题 5: 未收到通知

### 排查

```bash
# 1. 检查配置
kubectl get cm -n coredog-system coredog -o yaml | grep -A 5 NoticeChannel

# 2. 查看发送日志
kubectl logs -n coredog-system -l app.kubernetes.io/component=watcher | grep -i notice
```

### 原因

- NoticeChannel 未配置或为空
- Webhook URL 错误
- Keyword 过滤导致未发送

## 问题 6: Webhook 不工作

### 排查

```bash
# 查看 webhook 日志
kubectl logs -n coredog-system -l app.kubernetes.io/component=webhook -f

# 检查健康状态
kubectl get pods -n coredog-system -l app.kubernetes.io/component=webhook

# 检查 webhook 配置
kubectl get mutatingwebhookconfiguration coredog -o yaml
```

### 解决

- Webhook Pod CrashLoopBackOff → 检查证书配置
- 证书错误 → 重新生成：`./scripts/generate-cert.sh`
- Webhook 超时 → 增加资源配置

## 调试技巧

### 进入容器调试

```bash
# 进入 watcher Pod（现在有 bash 了）
kubectl exec -it -n coredog-system <watcher-pod> -- bash

# 查看目录
ls -la /corefile/

# 测试文件创建
echo "test" > /corefile/test-$(date +%s).txt

# 手动测试上传（如果需要）
```

### 查看详细事件日志

Watcher 会记录所有 fsnotify 事件：

```bash
kubectl logs -n coredog-system -l app.kubernetes.io/component=watcher -f
```

输出示例：
```
level=info msg="received fsnotify event: /corefile/core.xxx, op: CREATE"
level=info msg="capture a file:/corefile/core.xxx"
level=info msg="resolved pod from webhook path: namespace/podname"
level=info msg="deleted local corefile: /corefile/core.xxx"
```

## 紧急处理

### Webhook 阻塞 Pod 创建

```bash
# 临时删除 webhook 配置
kubectl delete mutatingwebhookconfiguration coredog

# 或修改为 Ignore 模式
kubectl patch mutatingwebhookconfiguration coredog \
  --type=json \
  -p='[{"op":"replace","path":"/webhooks/0/failurePolicy","value":"Ignore"}]'
```

### 磁盘满了

```bash
# 停止 watcher（暂停收集）
kubectl scale daemonset coredog-watcher -n coredog-system --replicas=0

# 清理文件
ssh <node>
find /data/coredog-system/dumps -type f -delete

# 恢复 watcher
kubectl scale daemonset coredog-watcher -n coredog-system --replicas=1
```

## 常用命令

```bash
# 查看 CoreDog 状态
kubectl get all -n coredog-system

# 查看配置
kubectl get cm -n coredog-system coredog -o yaml

# 重启 watcher
kubectl rollout restart daemonset -n coredog-system coredog-watcher

# 重启 webhook
kubectl rollout restart deployment -n coredog-system coredog-webhook

# 查看已开启的 Pod
kubectl get pods -A -o json | jq -r '.items[] | select(.metadata.annotations["coredog.io/inject"] == "true") | "\(.metadata.namespace)/\(.metadata.name)"'
```

