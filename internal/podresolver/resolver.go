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
func lookupNameByUID(namespace, uid string) (name string, ok bool) {
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

	pod, err := cli.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
		FieldSelector: "metadata.uid=" + uid,
		Limit:         1,
	})
	if err != nil || len(pod.Items) == 0 {
		logrus.Debugf("failed to find pod by UID %s in namespace %s: %v", uid, namespace, err)
		return "", false
	}

	return pod.Items[0].Name, true
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
