# CoreDog 文件检测问题排查指南

## 问题描述

如果您看到 CoreDog Watcher 日志中只有：
```
time="2025-10-14T04:09:27Z" level=info msg="started watch corefile in:/corefile"
```

但在 volume 中创建文件后没有任何后续日志输出，说明 CoreDog 没有检测到文件变化。

## 根本原因

CoreDog 使用 `fsnotify` 库来监听文件系统事件。但在以下场景中，`fsnotify` 可能无法正常工作：

1. **hostPath + core dump 组合**：Core dump 文件由宿主机内核直接写入，通过 hostPath 映射到容器，这种情况下容器内的 `fsnotify` 可能收不到事件
2. **只监听 CREATE 事件**：某些文件操作可能只触发 WRITE 事件而不触发 CREATE 事件
3. **跨挂载点**：文件系统挂载的边界可能影响 `fsnotify` 的行为

## 改进措施（v0.0.4+）

我们已经对 `internal/watcher/filewatch.go` 做了以下改进：

### 1. 增加详细日志

现在会记录**所有**收到的 `fsnotify` 事件：
```go
logrus.Infof("received fsnotify event: %s, op: %s", ev.Name, ev.Op.String())
```

这可以帮助您确认：
- ✅ `fsnotify` 是否收到了事件
- ✅ 收到的是什么类型的事件（CREATE, WRITE, REMOVE 等）

### 2. 支持 WRITE 事件

除了 `CREATE` 事件，现在也会处理 `WRITE` 事件：
```go
if ev.Op&fsnotify.Create == fsnotify.Create || ev.Op&fsnotify.Write == fsnotify.Write {
    // Process file
}
```

### 3. 防止重复处理

使用 `processedFiles` map 来跟踪已处理的文件，避免因多次 WRITE 事件导致同一个文件被重复上传。

## 排查步骤

### 步骤 1：确认 CoreDog Watcher 运行正常

```bash
# 检查 Pod 状态
kubectl get pods -n coredog-system -l app.kubernetes.io/component=watcher

# 查看日志
kubectl logs -n coredog-system -l app.kubernetes.io/component=watcher -f
```

应该看到：
```
level=info msg="started watch corefile in:/corefile"
```

### 步骤 2：测试文件检测（容器内创建）

进入 watcher Pod，在监听目录内直接创建文件：

```bash
# 获取 watcher Pod 名称
WATCHER_POD=$(kubectl get pods -n coredog-system -l app.kubernetes.io/component=watcher -o jsonpath='{.items[0].metadata.name}')

# 进入 Pod
kubectl exec -it -n coredog-system $WATCHER_POD -- sh

# 在容器内创建测试文件
cd /corefile
echo "test" > test-$(date +%s).txt

# 退出容器，查看日志
kubectl logs -n coredog-system $WATCHER_POD
```

**预期结果**：
- 如果看到 `received fsnotify event: /corefile/test-xxxxx.txt, op: CREATE` → ✅ `fsnotify` 工作正常
- 如果看到 `capture a file:/corefile/test-xxxxx.txt` → ✅ 文件检测成功

### 步骤 3：测试从宿主机创建文件

SSH 到 Kubernetes 节点，在宿主机上创建文件：

```bash
# 在节点上
echo "test from host" > /root/core/test-host-$(date +%s).txt
```

然后查看 CoreDog 日志，看是否能检测到。

**如果检测不到**：
- 这证实了 hostPath + fsnotify 的兼容性问题
- 需要考虑架构调整（见下文）

### 步骤 4：测试真实的 core dump

在应用 Pod 中触发真实的 core dump：

```bash
# 进入您的应用 Pod
kubectl exec -it <your-app-pod> -- bash

# 确认 ulimit 设置
ulimit -c
# 应该输出 "unlimited"

# 安装 gcc（如果需要）
apt-get update && apt-get install -y gcc

# 创建会崩溃的程序
cat > /tmp/crash.c <<'EOF'
#include <stdio.h>
int main() {
    printf("Generating core dump...\n");
    int *ptr = NULL;
    *ptr = 42;
    return 0;
}
EOF

gcc -o /tmp/crash /tmp/crash.c
/tmp/crash
```

然后检查：
1. 宿主机 `/root/core/` 是否有 core 文件生成
2. CoreDog 是否检测到并处理了

### 步骤 5：检查宿主机配置

```bash
# SSH 到节点
# 检查 core_pattern
cat /proc/sys/kernel/core_pattern

# 应该是类似：
# /root/core/core.%e.%p.%h.%t

# 检查目录权限
ls -ld /root/core
# 确保目录存在且可写
```

## 常见问题和解决方案

### 问题 1：完全没有 fsnotify 事件

**症状**：日志中只有 `started watch corefile`，即使在容器内创建文件也没有任何事件日志。

**可能原因**：
- 监听的目录路径不对
- 容器权限问题

**解决方案**：
```bash
# 检查 CoreDog 配置
kubectl get configmap -n coredog-system -o yaml | grep CorefileDir

# 进入 watcher Pod 确认目录
kubectl exec -it -n coredog-system $WATCHER_POD -- ls -la /corefile
```

### 问题 2：容器内创建文件能检测，宿主机创建不能检测

**症状**：容器内 `touch /corefile/test.txt` 能检测，但宿主机 `touch /root/core/test.txt` 不能。

**可能原因**：hostPath 挂载导致的 fsnotify 限制。

**解决方案**：
- **方案 A**：使用轮询机制（需要修改代码，见下文）
- **方案 B**：让 CoreDog Watcher 以 hostPID/hostNetwork 模式运行
- **方案 C**：使用 inotify 直接监听宿主机路径（需要特权容器）

### 问题 3：收到 WRITE 事件但没有 CREATE 事件

**症状**：日志中看到 `received fsnotify event: xxx, op: WRITE` 但没有 CREATE。

**说明**：这是正常的！v0.0.4+ 版本已经支持 WRITE 事件，文件应该能被正常处理。

## 架构改进建议

如果 `fsnotify` 在您的环境中无法可靠工作，可以考虑以下架构改进：

### 选项 1：添加轮询机制作为后备

在 `internal/watcher/filewatch.go` 中添加定期扫描：

```go
func (fw *FileWatcher) startPolling(dir string, interval time.Duration) {
    go func() {
        ticker := time.NewTicker(interval)
        knownFiles := make(map[string]bool)
        
        for range ticker.C {
            filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
                if err != nil || info.IsDir() {
                    return nil
                }
                if !knownFiles[path] && !fw.processedFiles[path] {
                    logrus.Infof("polling detected new file: %s", path)
                    knownFiles[path] = true
                    fw.receiver <- path
                }
                return nil
            })
        }
    }()
}
```

### 选项 2：使用特权容器直接监听宿主机

修改 `charts/templates/watcher.yaml`：

```yaml
securityContext:
  privileged: true
hostPID: true
hostNetwork: true
```

然后直接监听宿主机的 `/root/core` 而不是通过 hostPath 挂载。

### 选项 3：使用 Kubernetes Watch 机制

不监听文件系统，而是让应用在生成 core dump 后主动通知 CoreDog（通过 HTTP API 或 Kubernetes Event）。

## 验证修复

重新编译并部署 CoreDog 后：

```bash
# 重新构建镜像
make docker-build
docker push your-registry/coredog:v0.0.4

# 更新 values.yaml
# image.tag: v0.0.4

# 升级 Helm Chart
helm upgrade coredog ./charts -n coredog-system

# 测试
kubectl exec -it <app-pod> -- sh -c 'echo test > /corefile/test.txt'
kubectl logs -n coredog-system -l app.kubernetes.io/component=watcher
```

应该看到详细的事件日志。

## 参考资料

- [fsnotify GitHub](https://github.com/fsnotify/fsnotify)
- [Linux inotify 文档](https://man7.org/linux/man-pages/man7/inotify.7.html)
- [Kubernetes hostPath 卷](https://kubernetes.io/docs/concepts/storage/volumes/#hostpath)

