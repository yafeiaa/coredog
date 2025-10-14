# CoreDog 快速开始

## 5 分钟快速部署

### 1. 配置 values.yaml (2分钟)

```bash
vim charts/values.yaml
```

必填项：
```yaml
# S3 配置
s3AccesskeyID: "YOUR_KEY"
s3SecretAccessKey: "YOUR_SECRET"
s3Region: "ap-nanjing"
S3Bucket: "your-bucket"
S3Endpoint: "cos.ap-nanjing.myqcloud.com"

# 通知配置
NoticeChannel:
  - chan: wechat
    webhookurl: "https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=YOUR_KEY"
```

### 2. 配置节点 (1分钟)

在每个节点执行：
```bash
# 路径要与容器内挂载路径一致
# 如果 annotation 设置 coredog.io/path="/corefile"
echo '/corefile/core.%e.%p.%h.%t' > /proc/sys/kernel/core_pattern
```

### 3. 部署 (1分钟)

```bash
helm install coredog ./charts -n coredog-system --create-namespace
```

### 4. 应用接入 (1分钟)

给应用加 annotations：
```yaml
metadata:
  annotations:
    coredog.io/inject: "true"      # 必填
    coredog.io/path: "/corefile"   # 必填
    coredog.io/container: "app"    # 可选

spec:
  containers:
  - name: app
    command:
      - bash
      - -c
      - |
        ulimit -c unlimited  # 必须
        exec /app/server
```

### 5. 验证 (30秒)

```bash
# 查看注入
kubectl get pod <pod-name> -o yaml | grep coredog-corefile

# 触发崩溃测试
kubectl exec <pod> -- bash -c 'kill -11 $$'

# 查看日志
kubectl logs -n coredog-system -l app.kubernetes.io/component=watcher -f
```

## 完成！

应用崩溃时会自动：
1. 收集 core dump
2. 上传到 S3
3. 发送通知
4. 删除本地文件

详细文档见 [README.md](README.md)

