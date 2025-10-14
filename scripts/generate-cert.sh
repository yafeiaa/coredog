#!/bin/bash

# 生成 999 年有效的自签名证书用于 webhook
# 注意：这仅用于开发/测试环境，生产环境应使用正式的证书管理方案

set -e

NAMESPACE=${1:-coredog-system}
SERVICE_NAME=${2:-coredog-webhook}
OUTPUT_DIR=${3:-charts/webhook-certs}

echo "Generating webhook certificates for service: $SERVICE_NAME in namespace: $NAMESPACE"

# 创建输出目录
mkdir -p "$OUTPUT_DIR"

# 生成私钥
openssl genrsa -out "$OUTPUT_DIR/tls.key" 2048

# 生成证书签名请求（CSR）
cat > "$OUTPUT_DIR/csr.conf" <<EOF
[req]
req_extensions = v3_req
distinguished_name = req_distinguished_name
[req_distinguished_name]
[v3_req]
basicConstraints = CA:FALSE
keyUsage = nonRepudiation, digitalSignature, keyEncipherment
extendedKeyUsage = serverAuth
subjectAltName = @alt_names
[alt_names]
DNS.1 = ${SERVICE_NAME}
DNS.2 = ${SERVICE_NAME}.${NAMESPACE}
DNS.3 = ${SERVICE_NAME}.${NAMESPACE}.svc
DNS.4 = ${SERVICE_NAME}.${NAMESPACE}.svc.cluster.local
EOF

openssl req -new -key "$OUTPUT_DIR/tls.key" -subj "/CN=${SERVICE_NAME}.${NAMESPACE}.svc" -out "$OUTPUT_DIR/server.csr" -config "$OUTPUT_DIR/csr.conf"

# 生成自签名证书（999 年有效期）
openssl x509 -req -in "$OUTPUT_DIR/server.csr" -signkey "$OUTPUT_DIR/tls.key" -out "$OUTPUT_DIR/tls.crt" -days 364635 -extensions v3_req -extfile "$OUTPUT_DIR/csr.conf"

# 获取 CA Bundle（base64 编码的证书）
CA_BUNDLE=$(cat "$OUTPUT_DIR/tls.crt" | base64 | tr -d '\n')

echo ""
echo "✅ Certificates generated successfully!"
echo ""
echo "Files created:"
echo "  - $OUTPUT_DIR/tls.key"
echo "  - $OUTPUT_DIR/tls.crt"
echo ""
echo "Certificate details:"
openssl x509 -in "$OUTPUT_DIR/tls.crt" -noout -text | grep -A 2 "Validity"
echo ""
echo "CA Bundle (for webhook configuration):"
echo "$CA_BUNDLE"
echo ""
echo "To update the CA bundle in your MutatingWebhookConfiguration:"
echo "  caBundle: $CA_BUNDLE"

# 清理临时文件
rm -f "$OUTPUT_DIR/server.csr" "$OUTPUT_DIR/csr.conf"

