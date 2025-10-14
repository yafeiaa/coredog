package podresolver

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

type PodInfo struct {
	Name      string
	Namespace string
	UID       string
	Node      string
}

// Resolve extracts pod info from corefile path injected by webhook
// Webhook injects: /data/coredog-system/dumps/<namespace>/<pod-uid>/
// Watcher mounts: /data/coredog-system/dumps -> /corefile
// So watcher sees: /corefile/<namespace>/<pod-uid>/core.xxx
func Resolve(corefilePath string, enableLookup bool) PodInfo {
	info := PodInfo{Node: strings.TrimSpace(os.Getenv("NODE_NAME"))}

	logrus.Debugf("resolving pod info from corefile: %s", corefilePath)

	// Primary strategy: Extract from webhook-injected path pattern
	// Pattern: /corefile/<namespace>/<pod-uid>/...
	pathRegexp := regexp.MustCompile(`/corefile/([^/]+)/([0-9a-f-]{36})/`)
	if matches := pathRegexp.FindStringSubmatch(corefilePath); len(matches) == 3 {
		info.Namespace = matches[1]
		info.UID = matches[2]

		// 通过 K8s API 查询 Pod 名称
		if enableLookup {
			if podName, ok := lookupNameByUID(info.Namespace, info.UID); ok {
				info.Name = podName
				logrus.Infof("resolved pod from webhook path: %s/%s (uid: %s)", info.Namespace, info.Name, info.UID)
				return info
			}
		}

		// 如果 lookup 失败，只有 UID
		logrus.Warnf("resolved pod UID %s from path, but failed to get name", info.UID)
		return info
	}

	// Fallback: Parse filename for namespace_podname pattern (legacy)
	_, filename := filepath.Split(corefilePath)
	if parts := strings.Split(filename, "_"); len(parts) >= 2 {
		info.Namespace = parts[0]
		info.Name = parts[1]
		logrus.Infof("resolved pod from filename pattern: %s/%s", info.Namespace, info.Name)
		return info
	}

	logrus.Warnf("failed to resolve pod info for corefile: %s", corefilePath)
	return info
}

// lookupNameByUID gets Pod name from K8s API by namespace and UID
// 使用 client-go 的 metadata client 直接通过 UID 查询（高性能）
func lookupNameByUID(namespace, uid string) (name string, ok bool) {
	cfg, err := rest.InClusterConfig()
	if err != nil {
		logrus.Errorf("failed to get in-cluster config: %v", err)
		return "", false
	}

	cli, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		logrus.Errorf("failed to create k8s client: %v", err)
		return "", false
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	// 使用 FieldSelector 直接查询（性能最优）
	// 注意：metadata.uid 字段选择器在某些 Kubernetes 版本可能不支持
	// 如果不支持，回退到 List + 过滤（但加上节点过滤减少范围）
	nodeName := strings.TrimSpace(os.Getenv("NODE_NAME"))

	var fieldSelector string
	if nodeName != "" {
		// 限制在当前节点，大幅减少查询范围
		fieldSelector = "spec.nodeName=" + nodeName
	}

	podList, err := cli.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
		FieldSelector: fieldSelector,
	})
	if err != nil {
		logrus.Errorf("failed to list pods in namespace %s: %v", namespace, err)
		return "", false
	}

	// 遍历查找匹配的 UID（仅查询当前节点的 Pod，数量很少）
	for _, pod := range podList.Items {
		if string(pod.UID) == uid {
			logrus.Infof("found pod by UID: %s/%s (uid: %s)", namespace, pod.Name, uid)
			return pod.Name, true
		}
	}

	logrus.Warnf("pod with UID %s not found in namespace %s on node %s (searched %d pods)",
		uid, namespace, nodeName, len(podList.Items))
	return "", false
}

// lookupUIDByName gets Pod UID from K8s API by namespace and name
func lookupUIDByName(namespace, name string) (uid string, ok bool) {
	cfg, err := rest.InClusterConfig()
	if err != nil {
		logrus.Debugf("failed to get in-cluster config: %v", err)
		return "", false
	}

	cli, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		logrus.Debugf("failed to create k8s client: %v", err)
		return "", false
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	pod, err := cli.CoreV1().Pods(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		logrus.Debugf("failed to get pod %s/%s: %v", namespace, name, err)
		return "", false
	}

	return string(pod.UID), true
}
