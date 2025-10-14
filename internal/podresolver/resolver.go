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
// Webhook injects volumes with hostPath: /data/coredog-system/dumps/<namespace>/<podname>/
// Mounted to container at: /corefile (or custom path)
// Core dump file: /corefile/core.xxx (container view)
// Watcher sees: /corefile/core.xxx (because watcher mounts the parent hostPath)
//
// Wait, watcher needs to see the full path structure to parse namespace/podname!
// So watcher should mount: /data/coredog-system/dumps -> /corefile
// Then it will see: /corefile/<namespace>/<podname>/core.xxx
func Resolve(corefilePath string, enableLookup bool) PodInfo {
	info := PodInfo{Node: strings.TrimSpace(os.Getenv("NODE_NAME"))}

	logrus.Debugf("resolving pod info from corefile: %s", corefilePath)

	// Primary strategy: Extract from webhook-injected path pattern
	// Pattern: /corefile/<namespace>/<podname>/... (watcher's view)
	pathRegexp := regexp.MustCompile(`/corefile/([^/]+)/([^/]+)/`)
	if matches := pathRegexp.FindStringSubmatch(corefilePath); len(matches) == 3 {
		info.Namespace = matches[1]
		info.Name = matches[2]
		logrus.Infof("resolved pod from webhook path: %s/%s", info.Namespace, info.Name)

		// Optionally get UID via K8s API
		if enableLookup {
			if uid, ok := lookupUIDByName(info.Namespace, info.Name); ok {
				info.UID = uid
				logrus.Debugf("enriched pod UID via K8s API: %s", info.UID)
			}
		}
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
