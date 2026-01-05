package podresolver

import (
	"context"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

type PodInfo struct {
	Name          string
	Namespace     string
	UID           string
	NodeIP        string // Pod 所在节点的 IP，从 status.hostIP 获取
	Image         string
	ContainerName string
	IsLegacyPath  bool // 标记是否来自旧路径格式
}

// extractExecutableFromCorefile 从 coredump 文件名中提取可执行文件名
// 文件名格式: core.%e.%p.%h.%t (例如: core.bash.12345.hostname.1234567890)
func extractExecutableFromCorefile(corefilePath string) string {
	filename := filepath.Base(corefilePath)
	
	// 匹配格式: core.<executable>.<pid>.<hostname>.<timestamp>
	// 使用正则表达式提取可执行文件名
	re := regexp.MustCompile(`^core\.([^.]+)\..*`)
	matches := re.FindStringSubmatch(filename)
	if len(matches) >= 2 {
		executable := matches[1]
		logrus.Debugf("extracted executable '%s' from corefile: %s", executable, filename)
		return executable
	}
	
	logrus.Warnf("failed to extract executable from corefile: %s", filename)
	return ""
}

// Resolve 从 coredump 文件路径解析 Pod 和容器信息
// 支持新旧两种路径格式：
// 新格式：/data/coredog-system/dumps/<namespace>/<pod-name>/<container-name>/core.xxx
// 旧格式：/corefile/<namespace>/<admission-uid>/core.xxx
// 注意：NodeIP 必须通过 Kubernetes API 从 Pod 的 status.hostIP 获取
func Resolve(corefilePath string, enableLookup bool) PodInfo {
	info := PodInfo{}

	logrus.Debugf("resolving pod info from corefile: %s", corefilePath)

	// 1. 首先尝试从新的路径结构解析
	if newInfo := resolveFromNewPathStructure(corefilePath); newInfo.Name != "" {
		info = newInfo
		
		logrus.Infof("resolved pod from new path structure: %s/%s, container: %s", 
			info.Namespace, info.Name, info.ContainerName)
		
		// 必须通过 Kubernetes 查询获取 NodeIP 信息
		if enableLookup {
			if enrichedInfo := enrichPodInfoFromKubernetes(info); enrichedInfo.UID != "" {
				return enrichedInfo
			}
			logrus.Warnf("failed to get pod info from Kubernetes, NodeIP will be empty")
		}
		return info
	}

	// 2. 兼容旧的路径结构（admission-uid 方式）
	return resolveFromLegacyPath(corefilePath, enableLookup, info)
}

// resolveFromNewPathStructure 从新的路径结构解析 Pod 和容器信息
// 路径格式：
//   - 容器内：/corefile/<namespace>/<pod-name>/<container-name>/core.xxx
//   - 宿主机：/data/coredog-system/dumps/<namespace>/<pod-name>/<container-name>/core.xxx
func resolveFromNewPathStructure(corefilePath string) PodInfo {
	// 标准化路径
	cleanPath := filepath.Clean(corefilePath)
	
	var relativePath string
	
	// 尝试匹配 /dumps/ 路径（宿主机路径）
	if dumpsIndex := strings.Index(cleanPath, "/dumps/"); dumpsIndex != -1 {
		relativePath = cleanPath[dumpsIndex+7:] // 跳过 "/dumps/"
	} else if strings.HasPrefix(cleanPath, "/corefile/") {
		// 匹配 /corefile/ 路径（容器内路径）
		relativePath = cleanPath[10:] // 跳过 "/corefile/"
	} else {
		return PodInfo{}
	}
	
	parts := strings.Split(relativePath, "/")
	
	// 需要至少 3 个部分：namespace/pod-name/container-name
	if len(parts) < 3 {
		return PodInfo{}
	}
	
	namespace := parts[0]
	podName := parts[1] 
	containerName := parts[2]
	
	// 验证部分不为空
	if namespace == "" || podName == "" || containerName == "" {
		return PodInfo{}
	}
	
	logrus.Debugf("parsed new path structure: namespace=%s, pod=%s, container=%s", 
		namespace, podName, containerName)
	
	return PodInfo{
		Name:          podName,
		Namespace:     namespace,
		ContainerName: containerName,
	}
}

// resolveFromLegacyPath 从旧的路径结构解析（兼容性）
// 注意：NodeIP 通过 Kubernetes API 从 status.hostIP 获取
// 旧路径格式已废弃，会标记 IsLegacyPath=true 提示用户升级
func resolveFromLegacyPath(corefilePath string, enableLookup bool, info PodInfo) PodInfo {
	// 标记为旧路径格式
	info.IsLegacyPath = true

	// 从文件名中提取可执行文件名
	executable := extractExecutableFromCorefile(corefilePath)

	// 从路径提取 namespace 和 admission UID
	pathRegexp := regexp.MustCompile(`/corefile/([^/]+)/([0-9a-f-]{36})/`)
	if matches := pathRegexp.FindStringSubmatch(corefilePath); len(matches) == 3 {
		info.Namespace = matches[1]
		admissionUID := matches[2]

		// 通过 admission UID 查询 Pod（匹配 volume 路径）
		if enableLookup {
			if podName, podUID, image, hostIP, ok := lookupPodByAdmissionUID(info.Namespace, admissionUID, executable); ok {
				info.Name = podName
				info.UID = podUID
				info.Image = image
				info.NodeIP = hostIP
				logrus.Infof("resolved pod: %s/%s (admission-uid: %s, image: %s, hostIP: %s)", info.Namespace, info.Name, admissionUID, image, hostIP)
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

// enrichPodInfoFromKubernetes 从 Kubernetes API 获取详细的 Pod 信息
// NodeIP 从 status.hostIP 获取（Pod 实际运行所在节点的 IP）
func enrichPodInfoFromKubernetes(info PodInfo) PodInfo {
	config, err := rest.InClusterConfig()
	if err != nil {
		logrus.Warnf("failed to get in-cluster config: %v", err)
		return info
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		logrus.Warnf("failed to create kubernetes client: %v", err)
		return info
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// 查询指定的 Pod
	pod, err := clientset.CoreV1().Pods(info.Namespace).Get(ctx, info.Name, metav1.GetOptions{})
	if err != nil {
		logrus.Warnf("failed to get pod %s/%s: %v", info.Namespace, info.Name, err)
		return info
	}

	// 更新基本信息
	info.UID = string(pod.UID)
	info.NodeIP = pod.Status.HostIP // 从 status.hostIP 获取节点 IP

	// 查找对应容器的镜像
	if info.ContainerName != "" {
		// 在 containers 中查找
		for _, container := range pod.Spec.Containers {
			if container.Name == info.ContainerName {
				info.Image = container.Image
				logrus.Infof("found container %s with image: %s, hostIP: %s", info.ContainerName, info.Image, info.NodeIP)
				return info
			}
		}
		
		// 在 initContainers 中查找
		for _, container := range pod.Spec.InitContainers {
			if container.Name == info.ContainerName {
				info.Image = container.Image
				logrus.Infof("found init container %s with image: %s, hostIP: %s", info.ContainerName, info.Image, info.NodeIP)
				return info
			}
		}
		
		logrus.Warnf("container %s not found in pod %s/%s", info.ContainerName, info.Namespace, info.Name)
	}

	// 如果没有指定容器名或找不到，使用第一个容器的镜像作为默认值
	if len(pod.Spec.Containers) > 0 {
		info.Image = pod.Spec.Containers[0].Image
		logrus.Debugf("using first container image as default: %s", info.Image)
	}

	return info
}

// lookupPodByAdmissionUID 通过 annotation 查找 Pod，并根据可执行文件名匹配容器镜像
// annotation: coredog.io/admission-uid
// 返回 pod name, pod uid, image, hostIP (从 status.hostIP 获取)
// 注意：由于需要返回 hostIP 等动态信息，缓存仅用于减少日志，每次仍需查询 API
func lookupPodByAdmissionUID(namespace, admissionUID, executable string) (name string, uid string, image string, hostIP string, ok bool) {
	cfg, err := rest.InClusterConfig()
	if err != nil {
		logrus.Errorf("failed to get in-cluster config: %v", err)
		return "", "", "", "", false
	}

	cli, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		logrus.Errorf("failed to create k8s client: %v", err)
		return "", "", "", "", false
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	// 列出 namespace 中的所有 Pod
	podList, err := cli.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		logrus.Errorf("failed to list pods in namespace %s: %v", namespace, err)
		return "", "", "", "", false
	}

	// 遍历 Pod，查找 annotation 匹配的
	for _, pod := range podList.Items {
		if pod.Annotations["coredog.io/admission-uid"] == admissionUID {
			podName := pod.Name
			podUID := string(pod.UID)
			podHostIP := pod.Status.HostIP // 从 status.hostIP 获取节点 IP
			
			// 根据可执行文件名匹配对应的容器镜像
			var matchedImage string
			if executable != "" {
				matchedImage = findContainerImageByExecutable(pod.Spec.Containers, executable)
			}
			
			// 如果没有匹配到特定容器，使用第一个容器的镜像作为后备
			if matchedImage == "" && len(pod.Spec.Containers) > 0 {
				matchedImage = pod.Spec.Containers[0].Image
				logrus.Warnf("could not match executable '%s' to specific container, using first container image: %s", executable, matchedImage)
			}

			logrus.Infof("found pod by admission-uid annotation: %s/%s (pod-uid: %s, executable: %s, image: %s, hostIP: %s)", 
				namespace, podName, podUID, executable, matchedImage, podHostIP)
			return podName, podUID, matchedImage, podHostIP, true
		}
	}

	logrus.Warnf("pod with admission-uid %s not found (searched %d pods, pod may have been deleted)",
		admissionUID, len(podList.Items))
	return "", "", "", "", false
}

// findContainerImageByExecutable 根据可执行文件名匹配容器镜像
// 策略：
// 1. 如果可执行文件名与容器名匹配，返回该容器镜像
// 2. 如果可执行文件名与镜像名的一部分匹配，返回该容器镜像
// 3. 对于常见的可执行文件（如 java, python, node），尝试匹配对应的基础镜像
func findContainerImageByExecutable(containers []v1.Container, executable string) string {
	if executable == "" {
		return ""
	}
	
	// 策略1: 直接匹配容器名
	for _, container := range containers {
		if container.Name == executable {
			logrus.Debugf("matched executable '%s' to container name '%s', image: %s", executable, container.Name, container.Image)
			return container.Image
		}
	}
	
	// 策略2: 匹配镜像名的一部分
	for _, container := range containers {
		// 提取镜像名（去掉 registry 和 tag）
		imageName := extractImageName(container.Image)
		if strings.Contains(imageName, executable) || strings.Contains(executable, imageName) {
			logrus.Debugf("matched executable '%s' to image name '%s', full image: %s", executable, imageName, container.Image)
			return container.Image
		}
	}
	
	// 策略3: 基于常见可执行文件的启发式匹配
	executableLower := strings.ToLower(executable)
	for _, container := range containers {
		imageLower := strings.ToLower(container.Image)
		
		// Java 应用
		if (executableLower == "java" || strings.Contains(executableLower, "java")) && 
		   (strings.Contains(imageLower, "java") || strings.Contains(imageLower, "openjdk") || strings.Contains(imageLower, "jre")) {
			logrus.Debugf("matched Java executable '%s' to Java image: %s", executable, container.Image)
			return container.Image
		}
		
		// Python 应用
		if (executableLower == "python" || executableLower == "python3" || strings.HasPrefix(executableLower, "python")) && 
		   strings.Contains(imageLower, "python") {
			logrus.Debugf("matched Python executable '%s' to Python image: %s", executable, container.Image)
			return container.Image
		}
		
		// Node.js 应用
		if (executableLower == "node" || executableLower == "nodejs") && 
		   (strings.Contains(imageLower, "node") || strings.Contains(imageLower, "nodejs")) {
			logrus.Debugf("matched Node.js executable '%s' to Node.js image: %s", executable, container.Image)
			return container.Image
		}
		
		// Go 应用
		if strings.Contains(imageLower, "golang") || strings.Contains(imageLower, "go:") {
			logrus.Debugf("matched executable '%s' to Go image: %s", executable, container.Image)
			return container.Image
		}
	}
	
	logrus.Debugf("could not match executable '%s' to any specific container", executable)
	return ""
}

// extractImageName 从完整的镜像路径中提取镜像名
// 例如: registry.com/namespace/app:tag -> app
func extractImageName(fullImage string) string {
	// 移除 tag
	if idx := strings.LastIndex(fullImage, ":"); idx != -1 {
		fullImage = fullImage[:idx]
	}
	
	// 移除 registry 和 namespace
	if idx := strings.LastIndex(fullImage, "/"); idx != -1 {
		return fullImage[idx+1:]
	}
	
	return fullImage
}
