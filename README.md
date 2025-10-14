# CoreDog 🐶

Kubernetes Core Dump 自动收集系统

## 简介

CoreDog 在应用崩溃时自动收集 core dump 文件，上传到对象存储，并发送通知。

**核心特性**：
- 🎯 Webhook 自动注入 volume（opt-in，通过 annotation 开启）
- 🔍 精准识别崩溃的 Pod 和容器
- 📦 自动上传到 S3/COS/OSS
- 🧹 上传后自动清理本地文件
- 🔔 企业微信/Slack 即时通知

## 快速开始

### 1. 安装 CoreDog

```bash
# 编辑配置
vim charts/values.yaml
# 填写 S3 凭证和通知渠道（见下方配置说明）

# 安装
helm install coredog ./charts -n coredog-system --create-namespace
```

### 2. 配置节点

**在每个 Kubernetes 节点上执行**：

```bash
sudo su -

# ⚠️ 重要：路径要与容器内的挂载路径一致
# 如果 coredog.io/path="/corefile"，则配置为：
echo '/corefile/core.%e.%p.%h.%t' > /proc/sys/kernel/core_pattern

# 持久化
echo 'kernel.core_pattern=/corefile/core.%e.%p.%h.%t' >> /etc/sysctl.conf
sysctl -p

# 验证
cat /proc/sys/kernel/core_pattern
# 应该输出: /corefile/core.%e.%p.%h.%t
```

**说明**：
- 容器内挂载到 `/corefile`
- 内核配置也是 `/corefile/core.xxx`
- 由于 hostPath volume 映射，文件实际写到宿主机的 `/data/coredog-system/dumps/<ns>/<pod>/core.xxx`

### 3. 应用接入

在您的应用 Deployment/StatefulSet 中添加 annotations：

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: my-app
spec:
  template:
    metadata:
      annotations:
        # ⚠️ 必填：开启 CoreDog
        coredog.io/inject: "true"
        # ⚠️ 必填：指定挂载路径
        coredog.io/path: "/corefile"
        # 可选：指定要监控的容器（不填则所有容器）
        coredog.io/container: "app"
    spec:
      containers:
      - name: app
        image: my-app:v1
        command:
          - bash
          - -c
          - |
            ulimit -c unlimited  # ⚠️ 必须设置
            exec /app/server
```

**就这么简单！** 应用崩溃时会自动收集 core dump。

## 配置说明

### values.yaml 必填配置

编辑 `charts/values.yaml`：

```yaml
config:
  coredog: |-
    StorageConfig:
      # ⚠️ S3 配置（必填）
      s3AccesskeyID: "YOUR_ACCESS_KEY"
      s3SecretAccessKey: "YOUR_SECRET_KEY"
      s3Region: "ap-nanjing"
      S3Bucket: "your-bucket"
      S3Endpoint: "cos.ap-nanjing.myqcloud.com"
      
      # 上传后删除本地文件（强烈推荐）
      DeleteLocalCorefile: true
    
    # ⚠️ 通知渠道（至少配置一个）
    NoticeChannel:
      - chan: wechat
        webhookurl: "https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=YOUR_KEY"
```

### Annotations 配置

| Annotation | 必填 | 说明 | 示例 |
|-----------|------|------|------|
| `coredog.io/inject` | ✅ | 是否开启注入 | `"true"` |
| `coredog.io/path` | ✅ | Core dump 挂载路径 | `"/corefile"` |
| `coredog.io/container` | ❌ | 指定容器（逗号分隔），不填=所有容器 | `"app,worker"` |

### 路径安全限制

以下路径不允许使用（安全考虑）：
- `/`, `/etc`, `/usr`, `/bin`, `/sbin`, `/var`, `/root`, `/home`, `/boot`

推荐使用：
- `/corefile` ✅
- `/data/dumps` ✅
- `/app/coredumps` ✅

## 使用场景

### 场景 1: 单容器应用

```yaml
metadata:
  annotations:
    coredog.io/inject: "true"
    coredog.io/path: "/corefile"
spec:
  containers:
  - name: app
    image: my-app:v1
```

### 场景 2: 多容器 Pod - 只监控特定容器

```yaml
metadata:
  annotations:
    coredog.io/inject: "true"
    coredog.io/path: "/corefile"
    coredog.io/container: "gamesvr,dbproxy"  # 只监控业务容器
spec:
  containers:
  - name: gamesvr      # ✅ 会被注入
  - name: dbproxy      # ✅ 会被注入
  - name: nginx        # ❌ 不会被注入
  - name: metrics      # ❌ 不会被注入
```

### 场景 3: 自定义路径

```yaml
metadata:
  annotations:
    coredog.io/inject: "true"
    coredog.io/path: "/data/dumps"  # 自定义路径
spec:
  containers:
  - name: app
    command:
      - bash
      - -c
      - |
        ulimit -c unlimited
        cd /data/dumps  # 确保路径一致
        exec /app/server
```

## 架构说明

```
Pod 创建 → Webhook 拦截 → 检查 annotations
                            ↓
                    inject=true 且 path 已设置？
                            ↓
                    注入 volume 和 volumeMount
                            ↓
                    hostPath: /data/coredog-system/dumps/<ns>/<pod>/
                    mountPath: <path annotation>
                            ↓
应用崩溃 → 生成 core dump → /data/coredog-system/dumps/<ns>/<pod>/core.xxx
                            ↓
                    Watcher 检测到文件
                            ↓
                    从路径解析: namespace + podname
                            ↓
                    上传到 S3 → 删除本地文件 → 发送通知
```

## 验证和测试

### 验证注入

```bash
# 创建测试 Pod
kubectl run test --image=ubuntu \
  --annotations="coredog.io/inject=true,coredog.io/path=/corefile" \
  -- sleep 3600

# 检查是否注入成功
kubectl get pod test -o yaml | grep -A 5 coredog-corefile

# 应该看到：
# - name: coredog-corefile
#   hostPath:
#     path: /data/coredog-system/dumps/default/test
```

### 测试收集

```bash
# 创建会崩溃的测试应用
cat <<EOF | kubectl apply -f -
apiVersion: v1
kind: Pod
metadata:
  name: crash-test
  annotations:
    coredog.io/inject: "true"
    coredog.io/path: "/corefile"
spec:
  containers:
  - name: app
    image: ubuntu:22.04
    command:
      - bash
      - -c
      - |
        ulimit -c unlimited
        sleep 5
        kill -11 \$\$  # 触发段错误
EOF

# 查看收集日志
kubectl logs -n coredog-system -l app.kubernetes.io/component=watcher -f
```

**期望看到**：
```
level=info msg="capture a file:/corefile/core.bash.xxx"
level=info msg="resolved pod from webhook path: default/crash-test"
level=info msg="deleted local corefile: /corefile/core.bash.xxx"
```

## 故障排查

### Pod 未被注入

**检查**：
```bash
# 1. 查看 webhook 日志
kubectl logs -n coredog-system -l app.kubernetes.io/component=webhook

# 应该看到类似：
# Skip injection for pod default/my-pod - Reason: annotation coredog.io/path is required but not set
```

**常见原因**：
- ❌ 忘记添加 `coredog.io/inject: "true"`
- ❌ 忘记添加 `coredog.io/path`
- ❌ path 使用了危险路径（如 `/etc`）

### Core Dump 未被检测

**检查**：
```bash
# 1. 验证节点配置
cat /proc/sys/kernel/core_pattern
# 应该是: /data/coredog-system/dumps/%E/%E.%p.%h.%t

# 2. 验证 ulimit
kubectl exec <pod> -c <container> -- bash -c "ulimit -c"
# 应该是: unlimited

# 3. 查看 watcher 日志
kubectl logs -n coredog-system -l app.kubernetes.io/component=watcher -f
```

### Pod 信息识别失败

**现象**：通知显示 `[/] core: xxx` 而不是 `[namespace/podname]`

**原因**：路径格式不符合预期

**解决**：
- 确认 Pod 有正确的 annotations
- 确认 Webhook 正常工作
- 检查文件实际路径是否为 `/data/coredog-system/dumps/<ns>/<pod>/core.xxx`

### 本地文件未清理

**检查**：
```bash
# 查看配置
kubectl get cm -n coredog-system coredog -o yaml | grep DeleteLocalCorefile
# 应该是: true

# 查看日志中是否有删除记录
kubectl logs -n coredog-system -l app.kubernetes.io/component=watcher | grep "deleted local corefile"
```

## 通知配置

### 企业微信

```yaml
NoticeChannel:
  - chan: wechat
    webhookurl: "https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=xxx"
    keyword: ""  # 不过滤
```

### Slack

```yaml
NoticeChannel:
  - chan: slack
    webhookurl: "https://hooks.slack.com/services/xxx"
```

### 多渠道 + 过滤

```yaml
NoticeChannel:
  # 所有环境
  - chan: wechat
    webhookurl: "https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=ALL"
    keyword: ""
  
  # 只通知生产环境
  - chan: wechat
    webhookurl: "https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=PROD"
    keyword: "production"
```

### 自定义消息

```yaml
messageTemplate: |
  🚨 应用崩溃
  Pod: {pod.namespace}/{pod.name}
  节点: {pod.node}
  文件: {corefile.filename}
  下载: {corefile.url}
```

**可用变量**：
- `{pod.namespace}`, `{pod.name}`, `{pod.uid}`, `{pod.node}`
- `{host.ip}`
- `{corefile.path}`, `{corefile.filename}`, `{corefile.url}`

## 运维管理

### 查看已开启 CoreDog 的 Pod

```bash
kubectl get pods -A -o json | jq -r '.items[] | select(.metadata.annotations["coredog.io/inject"] == "true") | "\(.metadata.namespace)/\(.metadata.name)"'
```

### 批量开启

```bash
kubectl patch deployment my-app -p '{"spec":{"template":{"metadata":{"annotations":{"coredog.io/inject":"true","coredog.io/path":"/corefile"}}}}}'
```

### 升级

```bash
helm upgrade coredog ./charts -n coredog-system
```

### 卸载

```bash
helm uninstall coredog -n coredog-system
kubectl delete mutatingwebhookconfiguration coredog
```

## 文档

- [故障排查指南](docs/troubleshooting.md)

## 许可证

Apache License 2.0
