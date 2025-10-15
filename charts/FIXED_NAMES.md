# CoreDog Chart 固定名称配置

## 概述

为了确保 Webhook 证书的域名与实际服务名称匹配，我们将 Helm Chart 中的关键资源名称和 namespace 固定为特定值。

## 固定的资源名称

### Namespace
- **固定值**: `coredog-system`
- **说明**: 所有 CoreDog 组件都部署在此 namespace

### Service
- **Webhook Service**: `coredog-webhook`
- **说明**: 与证书中的 SAN 域名匹配

### Deployment
- **Webhook Deployment**: `coredog-webhook`
- **Watcher DaemonSet**: `coredog-watcher`

### ConfigMap
- **配置名称**: `coredog-config`

### Secret
- **证书 Secret**: `coredog-webhook-certs`

### ServiceAccount
- **服务账号**: `coredog`

### RBAC
- **ClusterRole**: `coredog-reader`
- **ClusterRoleBinding**: `coredog-reader-binding`

### MutatingWebhookConfiguration
- **配置名称**: `coredog-webhook-config`

## 证书域名信息

证书支持以下域名（自动生成）：
- `coredog-webhook`
- `coredog-webhook.coredog-system`
- `coredog-webhook.coredog-system.svc`
- `coredog-webhook.coredog-system.svc.cluster.local`

### 证书生成

```bash
./scripts/generate-cert.sh coredog-system coredog-webhook charts/webhook-certs
```

## 修改的文件列表

1. **charts/templates/mutatingwebhook.yaml**
   - `metadata.name`: 固定为 `coredog-webhook-config`
   - `clientConfig.service.name`: 固定为 `coredog-webhook`
   - `clientConfig.service.namespace`: 固定为 `coredog-system`

2. **charts/templates/webhook-deployment.yaml**
   - `metadata.name`: 固定为 `coredog-webhook`
   - `metadata.namespace`: 添加 `coredog-system`
   - `spec.template.spec.serviceAccountName`: 固定为 `coredog`
   - ConfigMap 引用: 固定为 `coredog-config`
   - Secret 引用: 固定为 `coredog-webhook-certs`

3. **charts/templates/webhook-service.yaml**
   - `metadata.name`: 固定为 `coredog-webhook`（已是固定值）
   - `metadata.namespace`: 添加 `coredog-system`

4. **charts/templates/webhook-secret.yaml**
   - `metadata.name`: 固定为 `coredog-webhook-certs`
   - `metadata.namespace`: 添加 `coredog-system`

5. **charts/templates/configmap.yaml**
   - `metadata.name`: 固定为 `coredog-config`
   - `metadata.namespace`: 添加 `coredog-system`

6. **charts/templates/serviceaccount.yaml**
   - `metadata.name`: 固定为 `coredog`
   - `metadata.namespace`: 添加 `coredog-system`

7. **charts/templates/rbac.yaml**
   - ClusterRole name: 固定为 `coredog-reader`
   - ClusterRoleBinding name: 固定为 `coredog-reader-binding`
   - ServiceAccount 引用: 固定为 `coredog`，namespace 为 `coredog-system`

8. **charts/templates/watcher.yaml**
   - `metadata.name`: 固定为 `coredog-watcher`
   - `metadata.namespace`: 添加 `coredog-system`
   - `spec.template.spec.serviceAccountName`: 固定为 `coredog`
   - ConfigMap 引用: 固定为 `coredog-config`

## 部署说明

### 前置条件

1. **生成证书**（如果还没有）：
   ```bash
   ./scripts/generate-cert.sh coredog-system coredog-webhook charts/webhook-certs
   ```

2. **创建 Namespace**：
   ```bash
   kubectl create namespace coredog-system
   ```

### 安装 Chart

```bash
helm install coredog ./charts -n coredog-system
```

或者升级现有安装：

```bash
helm upgrade coredog ./charts -n coredog-system
```

### 验证安装

1. **检查 Webhook Service**：
   ```bash
   kubectl get svc -n coredog-system coredog-webhook
   ```

2. **检查证书**：
   ```bash
   kubectl get secret -n coredog-system coredog-webhook-certs
   ```

3. **检查 MutatingWebhookConfiguration**：
   ```bash
   kubectl get mutatingwebhookconfiguration coredog-webhook-config
   ```

4. **验证证书域名**：
   ```bash
   kubectl get secret -n coredog-system coredog-webhook-certs -o jsonpath='{.data.tls\.crt}' | base64 -d | openssl x509 -noout -text | grep DNS
   ```
   
   应该输出：
   ```
   DNS:coredog-webhook, DNS:coredog-webhook.coredog-system, DNS:coredog-webhook.coredog-system.svc, DNS:coredog-webhook.coredog-system.svc.cluster.local
   ```

## 注意事项

1. **Namespace 固定**: 所有资源都必须部署在 `coredog-system` namespace
2. **证书匹配**: 证书中的域名已固定，不要修改 Service 名称
3. **升级影响**: 如果之前使用动态名称部署，升级时可能会创建新资源
4. **删除旧资源**: 升级后可能需要手动删除旧的资源（使用旧名称的）

## 清理旧资源

如果之前使用了动态名称，可能需要清理旧资源：

```bash
# 列出所有 MutatingWebhookConfiguration
kubectl get mutatingwebhookconfiguration

# 删除旧的（如果存在）
kubectl delete mutatingwebhookconfiguration <old-name>

# 列出 namespace 中的所有资源
kubectl get all -n coredog-system

# 根据需要删除旧的资源
```

## 故障排查

### Webhook 无法连接

如果 Webhook 无法正常工作，检查：

1. **Service 名称是否正确**：
   ```bash
   kubectl get svc -n coredog-system coredog-webhook
   ```

2. **Pod 是否运行**：
   ```bash
   kubectl get pods -n coredog-system -l app.kubernetes.io/component=webhook
   ```

3. **证书是否有效**：
   ```bash
   kubectl logs -n coredog-system deployment/coredog-webhook
   ```

4. **Webhook 配置是否正确**：
   ```bash
   kubectl describe mutatingwebhookconfiguration coredog-webhook-config
   ```

### 证书不匹配错误

如果出现证书域名不匹配错误：

1. 确认 Service 名称是 `coredog-webhook`
2. 确认 namespace 是 `coredog-system`
3. 重新生成证书（使用正确的参数）
4. 重新部署 Chart

