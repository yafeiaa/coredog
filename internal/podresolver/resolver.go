package podresolver

import (
	"context"
	"os"
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

// 缓存 admission-uid -> Pod Name 映射
var (
	admissionUIDCache = make(map[string]string)
	cacheMutex        sync.RWMutex
	cacheTTL          = 5 * time.Minute
	cacheExpiry       = make(map[string]time.Time)
)

// Resolve extracts pod info from corefile path injected by webhook
// Path: /corefile/<namespace>/<admission-uid>/core.xxx
func Resolve(corefilePath string, enableLookup bool) PodInfo {
	info := PodInfo{Node: strings.TrimSpace(os.Getenv("NODE_NAME"))}

	logrus.Debugf("resolving pod info from corefile: %s", corefilePath)

	// 从路径提取 namespace 和 admission UID
	pathRegexp := regexp.MustCompile(`/corefile/([^/]+)/([0-9a-f-]{36})/`)
	if matches := pathRegexp.FindStringSubmatch(corefilePath); len(matches) == 3 {
		info.Namespace = matches[1]
		admissionUID := matches[2]

		// 通过 admission UID 查询 Pod（匹配 volume 路径）
		if enableLookup {
			if podName, podUID, ok := lookupPodByAdmissionUID(info.Namespace, admissionUID); ok {
				info.Name = podName
				info.UID = podUID
				logrus.Infof("resolved pod: %s/%s (admission-uid: %s)", info.Namespace, info.Name, admissionUID)
				return info
			}
		}

		// 查询失败，使用 admission UID 前缀作为标识
		info.Name = "pod-" + admissionUID[:8]
		logrus.Warnf("pod with admission-uid %s not found, using prefix as name", admissionUID)
		return info
	}

	logrus.Warnf("failed to resolve pod info from path: %s", corefilePath)
	return info
}

// lookupPodByAdmissionUID 通过 annotation 查找 Pod
// annotation: coredog.io/admission-uid
// 只查询当前节点的 Pod（数量少，性能可接受）
func lookupPodByAdmissionUID(namespace, admissionUID string) (name string, uid string, ok bool) {
	cacheKey := namespace + "/" + admissionUID

	// 检查缓存
	cacheMutex.RLock()
	if cachedName, exists := admissionUIDCache[cacheKey]; exists {
		if expiry, hasExpiry := cacheExpiry[cacheKey]; hasExpiry && time.Now().Before(expiry) {
			cacheMutex.RUnlock()
			return cachedName, "", true
		}
	}
	cacheMutex.RUnlock()

	cfg, err := rest.InClusterConfig()
	if err != nil {
		logrus.Errorf("failed to get in-cluster config: %v", err)
		return "", "", false
	}

	cli, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		logrus.Errorf("failed to create k8s client: %v", err)
		return "", "", false
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	// 只查询当前节点的 Pod（范围小，性能可接受）
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
		return "", "", false
	}

	// 遍历 Pod，查找 annotation 匹配的
	for _, pod := range podList.Items {
		if pod.Annotations["coredog.io/admission-uid"] == admissionUID {
			podName := pod.Name
			podUID := string(pod.UID)

			// 更新缓存
			cacheMutex.Lock()
			admissionUIDCache[cacheKey] = podName
			cacheExpiry[cacheKey] = time.Now().Add(cacheTTL)
			cacheMutex.Unlock()

			logrus.Infof("found pod by admission-uid annotation: %s/%s (pod-uid: %s)", namespace, podName, podUID)
			return podName, podUID, true
		}
	}

	logrus.Warnf("pod with admission-uid %s not found on node %s (searched %d pods, pod may have been deleted)",
		admissionUID, nodeName, len(podList.Items))
	return "", "", false
}
