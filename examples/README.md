# CoreDog 示例

本目录包含使用 CoreDog 监控 core dump 的示例配置。

## 前提条件

在使用这些示例之前，您需要：

1. **安装 CoreDog Helm Chart**
   ```bash
   helm install coredog ./charts --namespace coredog-system --create-namespace
   ```

2. **配置宿主机的 core_pattern**（非常重要！）
   
   CoreDog Watcher 会监听 `/root/core` 目录（或您配置的其他目录）。您需要在**每个 Kubernetes 节点**上配置内核参数，让 core dump 文件生成到这个目录：
   
   ```bash
   # 在每个 Kubernetes 节点上执行
   sudo su -
   
   # 设置 core dump 文件的生成路径和命名格式
   echo '/root/core/core.%e.%p.%h.%t' > /proc/sys/kernel/core_pattern
   
   # 验证配置
   cat /proc/sys/kernel/core_pattern
   
   # 创建目录（如果使用 DirectoryOrCreate，这一步是可选的）
   mkdir -p /root/core
   chmod 755 /root/core
   ```
   
   **core_pattern 格式说明：**
   - `%e`: 可执行文件名
   - `%p`: 进程 PID
   - `%h`: 主机名
   - `%t`: dump 时间戳（UNIX 时间）
   
   **持久化配置（可选）：**
   
   为了让配置在重启后仍然生效，可以添加到 `/etc/sysctl.conf`：
   ```bash
   echo 'kernel.core_pattern=/root/core/core.%e.%p.%h.%t' >> /etc/sysctl.conf
   sysctl -p
   ```

## 示例说明

### 1. 简单的 Pod 示例

部署一个基础的 Pod，挂载 core dump volume：

```bash
kubectl apply -f pod-with-coredump.yaml
```

这个文件包含三个示例：

#### a) 单个 Pod
最简单的示例，展示基本的 volume 挂载和 core dump 配置。

#### b) Deployment
在生产环境中更常用的 Deployment 形式，包含多个副本。

#### c) 测试 Job
一个专门用于测试 core dump 功能的 Job，会主动触发段错误来生成 core dump。

### 2. 测试 Core Dump 功能

#### 方法一：使用测试 Job

```bash
# 运行测试 Job
kubectl apply -f pod-with-coredump.yaml

# 查看 Job 的 Pod
kubectl get pods -l app=coredump-test

# 查看日志
kubectl logs -l app=coredump-test
```

#### 方法二：手动触发崩溃

```bash
# 进入正在运行的 Pod
kubectl exec -it example-app-with-coredump -- bash

# 在容器内执行以下命令来触发段错误
kill -SIGSEGV $$
```

或者编写一个会崩溃的程序：

```bash
# 在容器内
apt-get update && apt-get install -y gcc
cat > /tmp/crash.c <<'EOF'
#include <stdio.h>
int main() {
    printf("Generating core dump...\n");
    int *ptr = NULL;
    *ptr = 42;  // 段错误
    return 0;
}
EOF
gcc -o /tmp/crash /tmp/crash.c
ulimit -c unlimited
/tmp/crash
```

### 3. 验证 CoreDog 是否捕获到 Core Dump

```bash
# 查看 CoreDog Watcher 的日志
kubectl logs -n coredog-system -l app.kubernetes.io/component=watcher

# 检查宿主机上的 core 文件（需要 SSH 到节点）
ls -lh /root/core/

# 如果配置了 S3 存储，检查您的 S3 bucket
# aws s3 ls s3://your-bucket/corefiles/
```

## 重要配置项

### ulimit 设置

容器内必须设置 `ulimit -c unlimited` 才能生成 core dump：

```yaml
command:
  - bash
  - -c
  - |
    ulimit -c unlimited
    exec your-application
```

### SecurityContext

根据您的应用需求，可能需要添加特定的权限：

```yaml
securityContext:
  capabilities:
    add:
    - SYS_PTRACE  # 用于调试和生成 core dump
```

### Volume 路径

确保 Pod 的 volume 挂载路径与 CoreDog 配置中的 `CorefileDir` 一致：

```yaml
# CoreDog values.yaml
config:
  coredog: |-
    CorefileDir: /corefile

# Pod volume mount
volumeMounts:
- name: corefile
  mountPath: /corefile

# HostPath 必须与 coredog chart 的配置一致
volumes:
- name: corefile
  hostPath:
    path: /root/core  # 与 coredog chart values.yaml 中的 corefileVolume.hostPath.path 一致
```

## 故障排查

### Core Dump 文件没有生成

1. **检查 ulimit 设置**
   ```bash
   kubectl exec -it <pod-name> -- bash -c "ulimit -c"
   # 应该输出 "unlimited"
   ```

2. **检查宿主机的 core_pattern**
   ```bash
   # SSH 到节点
   cat /proc/sys/kernel/core_pattern
   ```

3. **检查目录权限**
   ```bash
   # 在节点上
   ls -ld /root/core
   # 确保目录存在且有写权限
   ```

### CoreDog 没有检测到 Core Dump

1. **检查 Watcher 是否运行**
   ```bash
   kubectl get pods -n coredog-system
   ```

2. **查看 Watcher 日志**
   ```bash
   kubectl logs -n coredog-system -l app.kubernetes.io/component=watcher -f
   ```

3. **确认监听的目录是否正确**
   ```bash
   kubectl get configmap -n coredog-system -o yaml
   # 检查 CorefileDir 配置
   ```

## 生产环境建议

1. **资源限制**：为应用设置合理的资源限制，避免 core dump 文件过大影响节点磁盘。

2. **自动清理**：在 CoreDog 配置中启用 `gc: true` 和 `DeleteLocalCorefile: true`，上传后自动删除本地文件。

3. **存储配置**：配置 S3 或其他对象存储，避免 core dump 文件占满节点磁盘。

4. **告警通知**：配置 CoreDog 的 NoticeChannel，在生成 core dump 时及时通知开发团队。

5. **权限最小化**：仅在需要的容器中添加 SYS_PTRACE 等特殊权限。

## 参考资料

- [Linux Core Dump 配置](https://man7.org/linux/man-pages/man5/core.5.html)
- [Kubernetes SecurityContext](https://kubernetes.io/docs/tasks/configure-pod-container/security-context/)
- [CoreDog GitHub](https://github.com/DomineCore/coredog)

