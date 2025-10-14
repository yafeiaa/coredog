package podresolver

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
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

// 缓存 UID -> Name 映射，避免重复查询
var (
	uidToNameCache = make(map[string]string)
	cacheMutex     sync.RWMutex
	cacheTTL       = 5 * time.Minute
	cacheExpiry    = make(map[string]time.Time)
)

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
// 带缓存，避免重复查询
func lookupNameByUID(namespace, uid string) (name string, ok bool) {
	cacheKey := namespace + "/" + uid

	// 检查缓存
	cacheMutex.RLock()
	if cachedName, exists := uidToNameCache[cacheKey]; exists {
		if expiry, hasExpiry := cacheExpiry[cacheKey]; hasExpiry && time.Now().Before(expiry) {
			cacheMutex.RUnlock()
			logrus.Debugf("found pod in cache: %s/%s (uid: %s)", namespace, cachedName, uid)
			return cachedName, true
		}
	}
	cacheMutex.RUnlock()

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

	// 限制在当前节点，大幅减少查询范围
	nodeName := strings.TrimSpace(os.Getenv("NODE_NAME"))
	var fieldSelector string
	if nodeName != "" {
		fieldSelector = "spec.nodeName=" + nodeName
	}

	podList, err := cli.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
		FieldSelector: fieldSelector,
	})
	if err != nil {
		logrus.Errorf("failed to list pods in namespace %s: %v", namespace, err)
		return "", false
	}

	// 遍历查找匹配的 UID
	for _, pod := range podList.Items {
		if string(pod.UID) == uid {
			podName := pod.Name

			// 更新缓存
			cacheMutex.Lock()
			uidToNameCache[cacheKey] = podName
			cacheExpiry[cacheKey] = time.Now().Add(cacheTTL)
			cacheMutex.Unlock()

			logrus.Infof("found pod by UID: %s/%s (uid: %s)", namespace, podName, uid)
			return podName, true
		}
	}

	logrus.Warnf("pod with UID %s not found in namespace %s on node %s (searched %d pods, pod may have been deleted)",
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
