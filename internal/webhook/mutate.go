package webhook

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"strings"

	cfgpkg "github.com/DomineCore/coredog/internal/config"
	"github.com/sirupsen/logrus"
	admissionv1 "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
)

const (
	CorefileVolumeName         = "coredog-corefile"
	CorefileMountPath          = "/corefile"            // 默认挂载路径
	CoredogAnnotationInject    = "coredog.io/inject"    // "true" 开启注入
	CoredogAnnotationContainer = "coredog.io/container" // 指定容器名，多个用逗号分隔
	CoredogAnnotationPath      = "coredog.io/path"      // core dump 路径，默认 /corefile
	CoredogPathBase            = "/data/coredog-system/dumps"
)

var (
	runtimeScheme = runtime.NewScheme()
	codecs        = serializer.NewCodecFactory(runtimeScheme)
	deserializer  = codecs.UniversalDeserializer()
)

func init() {
	_ = corev1.AddToScheme(runtimeScheme)
	_ = admissionv1.AddToScheme(runtimeScheme)
}

type MutateHandler struct {
	// 可以添加配置选项
	Enabled    bool
	PathBase   string
	MountPath  string
	VolumeName string
}

func NewMutateHandler() *MutateHandler {
	return &MutateHandler{
		Enabled:    true,
		PathBase:   CoredogPathBase,
		MountPath:  CorefileMountPath,
		VolumeName: CorefileVolumeName,
	}
}

func (h *MutateHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var body []byte
	if r.Body != nil {
		if data, err := ioutil.ReadAll(r.Body); err == nil {
			body = data
		}
	}

	if len(body) == 0 {
		logrus.Error("empty body")
		http.Error(w, "empty body", http.StatusBadRequest)
		return
	}

	contentType := r.Header.Get("Content-Type")
	if contentType != "application/json" {
		logrus.Errorf("Content-Type=%s, expect application/json", contentType)
		http.Error(w, "invalid Content-Type, expect `application/json`", http.StatusUnsupportedMediaType)
		return
	}

	var admissionResponse *admissionv1.AdmissionResponse
	ar := admissionv1.AdmissionReview{}
	if _, _, err := deserializer.Decode(body, nil, &ar); err != nil {
		logrus.Errorf("Can't decode body: %v", err)
		admissionResponse = &admissionv1.AdmissionResponse{
			Result: &metav1.Status{
				Message: err.Error(),
			},
		}
	} else {
		admissionResponse = h.mutatePods(&ar)
	}

	admissionReview := admissionv1.AdmissionReview{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "admission.k8s.io/v1",
			Kind:       "AdmissionReview",
		},
	}
	if admissionResponse != nil {
		admissionReview.Response = admissionResponse
		if ar.Request != nil {
			admissionReview.Response.UID = ar.Request.UID
		}
	}

	resp, err := json.Marshal(admissionReview)
	if err != nil {
		logrus.Errorf("Can't encode response: %v", err)
		http.Error(w, fmt.Sprintf("could not encode response: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if _, err := w.Write(resp); err != nil {
		logrus.Errorf("Can't write response: %v", err)
		http.Error(w, fmt.Sprintf("could not write response: %v", err), http.StatusInternalServerError)
	}
}

func (h *MutateHandler) mutatePods(ar *admissionv1.AdmissionReview) *admissionv1.AdmissionResponse {
	req := ar.Request
	var pod corev1.Pod
	if err := json.Unmarshal(req.Object.Raw, &pod); err != nil {
		logrus.Errorf("Could not unmarshal raw object: %v", err)
		h.sendAlert(fmt.Sprintf("⚠️ CoreDog Webhook 解析失败\n"+
			"Pod: %s/%s\n"+
			"错误: %v\n"+
			"影响: Pod 将正常创建，但不会自动注入 core dump 收集功能，该 Pod 的 core dump 文件无法被 CoreDog 自动收集和识别",
			req.Namespace, req.Name, err))
		// 返回允许，不阻塞 Pod 创建
		return &admissionv1.AdmissionResponse{
			Allowed: true,
		}
	}

	logrus.Infof("AdmissionReview for Kind=%s, Namespace=%s Name=%s UID=%s Operation=%s",
		req.Kind.Kind, req.Namespace, req.Name, req.UID, req.Operation)

	// 检查是否需要注入（可以通过 annotation 控制）
	shouldInject, reason := h.shouldInject(&pod)
	if !shouldInject {
		logrus.Infof("Skip injection for pod %s/%s - Reason: %s", req.Namespace, req.Name, reason)
		return &admissionv1.AdmissionResponse{
			Allowed: true,
		}
	}

	// 生成 patch
	patches := h.createPatch(&pod, req)
	if len(patches) == 0 {
		return &admissionv1.AdmissionResponse{
			Allowed: true,
		}
	}

	patchBytes, err := json.Marshal(patches)
	if err != nil {
		logrus.Errorf("Could not marshal patches: %v", err)
		return &admissionv1.AdmissionResponse{
			Result: &metav1.Status{
				Message: err.Error(),
			},
		}
	}

	logrus.Infof("Injecting corefile volume for pod %s/%s (admission-uid: %s)", req.Namespace, pod.Name, req.UID)

	return &admissionv1.AdmissionResponse{
		Allowed: true,
		Patch:   patchBytes,
		PatchType: func() *admissionv1.PatchType {
			pt := admissionv1.PatchTypeJSONPatch
			return &pt
		}(),
	}
}

func (h *MutateHandler) shouldInject(pod *corev1.Pod) (bool, string) {
	// 检查 annotation
	if pod.Annotations == nil {
		return false, "no annotations"
	}

	// 必须显式设置 coredog.io/inject: "true" 才注入
	inject, exists := pod.Annotations[CoredogAnnotationInject]
	if !exists {
		return false, "annotation coredog.io/inject not found"
	}

	if inject != "true" {
		return false, fmt.Sprintf("annotation coredog.io/inject=%s (not 'true')", inject)
	}

	// ⚠️ 安全要求：必须明确指定 path
	path, pathExists := pod.Annotations[CoredogAnnotationPath]
	if !pathExists || strings.TrimSpace(path) == "" {
		return false, "annotation coredog.io/path is required but not set (security: prevent unintended mounts)"
	}

	// 检查路径是否合法（不能是危险路径）
	path = strings.TrimSpace(path)
	dangerousPaths := []string{"/", "/etc", "/usr", "/bin", "/sbin", "/var", "/root", "/home", "/boot"}
	for _, dangerous := range dangerousPaths {
		if path == dangerous || strings.HasPrefix(path, dangerous+"/") {
			return false, fmt.Sprintf("annotation coredog.io/path=%s is not allowed (security: dangerous path)", path)
		}
	}

	return true, ""
}

// getTargetContainers 获取需要注入 volumeMount 的容器列表
func (h *MutateHandler) getTargetContainers(pod *corev1.Pod) map[string]bool {
	targetMap := make(map[string]bool)

	// 检查是否指定了容器
	if containerNames, exists := pod.Annotations[CoredogAnnotationContainer]; exists && containerNames != "" {
		// 解析容器名列表（逗号分隔）
		for _, name := range strings.Split(containerNames, ",") {
			name = strings.TrimSpace(name)
			if name != "" {
				targetMap[name] = true
			}
		}
	} else {
		// 如果没有指定，默认注入所有容器
		for _, container := range pod.Spec.Containers {
			targetMap[container.Name] = true
		}
		for _, container := range pod.Spec.InitContainers {
			targetMap[container.Name] = true
		}
	}

	return targetMap
}

func (h *MutateHandler) createPatch(pod *corev1.Pod, req *admissionv1.AdmissionRequest) []map[string]interface{} {
	var patches []map[string]interface{}

	// 获取 core dump 路径配置（必填，已在 shouldInject 中验证）
	mountPath := strings.TrimSpace(pod.Annotations[CoredogAnnotationPath])

	// 获取要注入的容器列表
	targetContainers := h.getTargetContainers(pod)

	// 构建包含容器信息的路径结构
	// 新方案：/data/coredog-system/dumps/<namespace>/<pod-name>/<container-name>/
	// 这样 Agent 可以直接从路径知道容器信息，无需猜测
	
	// 使用 pod.Name，如果为空则使用 admission UID
	podName := pod.Name
	if podName == "" {
		podName = "pod-" + string(req.UID)[:8]
	}

	logrus.Infof("Creating container-aware paths for pod %s/%s", req.Namespace, podName)

	// 为每个目标容器创建独立的路径
	// 基础路径：/data/coredog-system/dumps/<namespace>/<pod-name>/
	basePath := fmt.Sprintf("%s/%s/%s", h.PathBase, req.Namespace, podName)

	// 添加 Pod 和容器信息到 annotations（用于后续通过路径反查）
	patches = append(patches, map[string]interface{}{
		"op":    "add",
		"path":  "/metadata/annotations/coredog.io~1pod-name",
		"value": podName,
	})
	
	// 记录目标容器列表
	var containerNames []string
	for containerName := range targetContainers {
		containerNames = append(containerNames, containerName)
	}
	patches = append(patches, map[string]interface{}{
		"op":    "add", 
		"path":  "/metadata/annotations/coredog.io~1target-containers",
		"value": strings.Join(containerNames, ","),
	})

	// 为每个目标容器创建独立的 volume 和 volumeMount
	// 这样每个容器的 coredump 会存储在独立的目录中
	volumeIndex := 0
	for containerName := range targetContainers {
		volumeName := fmt.Sprintf("%s-%s", h.VolumeName, containerName)
		containerPath := fmt.Sprintf("%s/%s", basePath, containerName)
		
		// 检查是否已经存在该 volume
		volumeExists := false
		for _, vol := range pod.Spec.Volumes {
			if vol.Name == volumeName {
				volumeExists = true
				break
			}
		}

		// 添加容器专用的 volume
		if !volumeExists {
			if len(pod.Spec.Volumes) == 0 && volumeIndex == 0 {
				// 如果 volumes 列表为空，创建新列表
				patches = append(patches, map[string]interface{}{
					"op":   "add",
					"path": "/spec/volumes",
					"value": []corev1.Volume{
						{
							Name: volumeName,
							VolumeSource: corev1.VolumeSource{
								HostPath: &corev1.HostPathVolumeSource{
									Path: containerPath,
									Type: func() *corev1.HostPathType {
										t := corev1.HostPathDirectoryOrCreate
										return &t
									}(),
								},
							},
						},
					},
				})
			} else {
				// 添加到现有列表
				patches = append(patches, map[string]interface{}{
					"op":   "add",
					"path": "/spec/volumes/-",
					"value": corev1.Volume{
						Name: volumeName,
						VolumeSource: corev1.VolumeSource{
							HostPath: &corev1.HostPathVolumeSource{
								Path: containerPath,
								Type: func() *corev1.HostPathType {
									t := corev1.HostPathDirectoryOrCreate
									return &t
								}(),
							},
						},
					},
				})
			}
		}
		volumeIndex++
	}

	// 为每个容器添加对应的 volumeMount
	for i := range pod.Spec.Containers {
		containerName := pod.Spec.Containers[i].Name

		// 检查是否是目标容器
		if !targetContainers[containerName] {
			continue
		}

		volumeName := fmt.Sprintf("%s-%s", h.VolumeName, containerName)
		
		mountExists := false
		for _, mount := range pod.Spec.Containers[i].VolumeMounts {
			if mount.Name == volumeName {
				mountExists = true
				break
			}
		}

		if !mountExists {
			if len(pod.Spec.Containers[i].VolumeMounts) == 0 {
				patches = append(patches, map[string]interface{}{
					"op":   "add",
					"path": fmt.Sprintf("/spec/containers/%d/volumeMounts", i),
					"value": []corev1.VolumeMount{
						{
							Name:      volumeName,
							MountPath: mountPath,
						},
					},
				})
			} else {
				patches = append(patches, map[string]interface{}{
					"op":   "add",
					"path": fmt.Sprintf("/spec/containers/%d/volumeMounts/-", i),
					"value": corev1.VolumeMount{
						Name:      volumeName,
						MountPath: mountPath,
					},
				})
			}
		}
	}

	// 同样处理 initContainers
	for i := range pod.Spec.InitContainers {
		containerName := pod.Spec.InitContainers[i].Name

		// 检查是否是目标容器
		if !targetContainers[containerName] {
			continue
		}

		volumeName := fmt.Sprintf("%s-%s", h.VolumeName, containerName)
		
		mountExists := false
		for _, mount := range pod.Spec.InitContainers[i].VolumeMounts {
			if mount.Name == volumeName {
				mountExists = true
				break
			}
		}

		if !mountExists {
			if len(pod.Spec.InitContainers[i].VolumeMounts) == 0 {
				patches = append(patches, map[string]interface{}{
					"op":   "add",
					"path": fmt.Sprintf("/spec/initContainers/%d/volumeMounts", i),
					"value": []corev1.VolumeMount{
						{
							Name:      volumeName,
							MountPath: mountPath,
						},
					},
				})
			} else {
				patches = append(patches, map[string]interface{}{
					"op":   "add",
					"path": fmt.Sprintf("/spec/initContainers/%d/volumeMounts/-", i),
					"value": corev1.VolumeMount{
						Name:      volumeName,
						MountPath: mountPath,
					},
				})
			}
		}
	}

	return patches
}

// sendAlert sends alert message to configured notification channels
// It reuses the NoticeChannel configuration from config file
func (h *MutateHandler) sendAlert(message string) {
	cfg := cfgpkg.Get()

	if len(cfg.NoticeChannel) == 0 {
		logrus.Warnf("NoticeChannel not configured, skip sending webhook alert")
		return
	}

	// 发送到所有配置的通知渠道
	for _, ch := range cfg.NoticeChannel {
		var payload []byte
		var err error

		switch ch.Chan {
		case "wechat":
			payload, err = json.Marshal(map[string]interface{}{
				"msgtype": "text",
				"text": map[string]string{
					"content": message,
				},
			})
		case "slack":
			payload, err = json.Marshal(map[string]interface{}{
				"text": message,
			})
		default:
			payload, err = json.Marshal(map[string]interface{}{
				"content": message,
			})
		}

		if err != nil {
			logrus.Errorf("Failed to marshal alert payload: %v", err)
			continue
		}

		resp, err := http.Post(ch.Webhookurl, "application/json", bytes.NewBuffer(payload))
		if err != nil {
			logrus.Errorf("Failed to send alert to %s: %v", ch.Chan, err)
			continue
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			body, _ := ioutil.ReadAll(resp.Body)
			logrus.Errorf("Alert webhook (%s) returned non-200 status: %d, body: %s", ch.Chan, resp.StatusCode, string(body))
		} else {
			logrus.Infof("Alert sent successfully to %s", ch.Chan)
		}
	}
}
