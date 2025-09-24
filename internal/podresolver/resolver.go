package podresolver

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

var podUIDRegexp = regexp.MustCompile(`pods/([0-9a-fA-F-]{36})`)

type PodInfo struct {
	Name      string
	Namespace string
	UID       string
	Node      string
}

// Resolve extracts pod info heuristically from corefile path, optionally enrich via K8s API.
func Resolve(corefilePath string, enableLookup bool) PodInfo {
	matches := podUIDRegexp.FindStringSubmatch(corefilePath)
	info := PodInfo{Node: strings.TrimSpace(os.Getenv("NODE_NAME"))}
	if len(matches) == 2 {
		info.UID = matches[1]
	}
	_, filename := filepath.Split(corefilePath)
	if parts := strings.Split(filename, "_"); len(parts) >= 2 {
		info.Namespace = parts[0]
		info.Name = parts[1]
	}
	if enableLookup && info.UID != "" {
		if ns, name, ok := lookupByUID(info.UID); ok {
			info.Namespace = ns
			info.Name = name
		}
	}
	return info
}

func lookupByUID(uid string) (namespace, name string, ok bool) {
	cfg, err := rest.InClusterConfig()
	if err != nil {
		return "", "", false
	}
	cli, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return "", "", false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	pods, err := cli.CoreV1().Pods("").List(ctx, metav1.ListOptions{Limit: 1, FieldSelector: "metadata.uid=" + uid})
	if err != nil || len(pods.Items) == 0 {
		return "", "", false
	}
	return pods.Items[0].Namespace, pods.Items[0].Name, true
}
