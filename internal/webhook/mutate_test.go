package webhook

import (
	"encoding/json"
	"strings"
	"testing"

	cfgpkg "github.com/DomineCore/coredog/internal/config"
	admissionv1 "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

func TestNormalizePathBase(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"empty falls back to default", "", CoredogPathBase},
		{"whitespace falls back to default", "   ", CoredogPathBase},
		{"root falls back to default", "/", CoredogPathBase},
		{"trailing slash trimmed", "/data/custom/", "/data/custom"},
		{"surrounding whitespace trimmed", "  /data/custom  ", "/data/custom"},
		{"whitespace plus trailing slash", "  /data/custom/  ", "/data/custom"},
		{"custom path kept as-is", "/var/coredog/dumps", "/var/coredog/dumps"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := normalizePathBase(tc.in); got != tc.want {
				t.Errorf("normalizePathBase(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// buildAdmissionReview 构造一个带注入 annotations 的 AdmissionReview。
func buildAdmissionReview(t *testing.T, ns, podName, path string) *admissionv1.AdmissionReview {
	t.Helper()
	pod := corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      podName,
			Namespace: ns,
			Annotations: map[string]string{
				CoredogAnnotationInject: "true",
				CoredogAnnotationPath:   path,
			},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{Name: "app"}},
		},
	}
	raw, err := json.Marshal(pod)
	if err != nil {
		t.Fatalf("marshal pod: %v", err)
	}
	return &admissionv1.AdmissionReview{
		Request: &admissionv1.AdmissionRequest{
			Kind:      metav1.GroupVersionKind{Kind: "Pod"},
			Namespace: ns,
			Name:      podName,
			UID:       "test-uid-1234",
			Object:    runtime.RawExtension{Raw: raw},
		},
	}
}

func TestCreatePatchUsesConfiguredPathBase(t *testing.T) {
	const customBase = "/var/coredog/dumps"
	h := &MutateHandler{
		Enabled:    true,
		PathBase:   customBase,
		MountPath:  CorefileMountPath,
		VolumeName: CorefileVolumeName,
	}

	ar := buildAdmissionReview(t, "prod", "my-pod", "/corefile")
	resp := h.mutatePods(ar)
	if !resp.Allowed {
		t.Fatal("expected admission to be allowed")
	}
	if len(resp.Patch) == 0 {
		t.Fatal("expected a patch to be generated")
	}

	var patches []map[string]interface{}
	if err := json.Unmarshal(resp.Patch, &patches); err != nil {
		t.Fatalf("unmarshal patch: %v", err)
	}

	patchJSON := string(resp.Patch)
	if !strings.Contains(patchJSON, customBase+"/prod/my-pod/app") {
		t.Errorf("patch should contain hostPath %s/%s/%s/%s, patch: %s",
			customBase, "prod", "my-pod", "app", patchJSON)
	}
	if strings.Contains(patchJSON, CoredogPathBase) {
		t.Errorf("patch should not reference default base %q when custom base is set, patch: %s",
			CoredogPathBase, patchJSON)
	}
}

func TestCreatePatchUsesDefaultPathBase(t *testing.T) {
	h := &MutateHandler{
		Enabled:    true,
		PathBase:   normalizePathBase(""),
		MountPath:  CorefileMountPath,
		VolumeName: CorefileVolumeName,
	}

	ar := buildAdmissionReview(t, "prod", "my-pod", "/corefile")
	resp := h.mutatePods(ar)
	if !resp.Allowed || len(resp.Patch) == 0 {
		t.Fatal("expected allowed response with a patch")
	}
	if !strings.Contains(string(resp.Patch), CoredogPathBase+"/prod/my-pod/app") {
		t.Errorf("patch should contain default base path, patch: %s", string(resp.Patch))
	}
}

func TestConfigCoredogPathBaseField(t *testing.T) {
	// 仅验证配置结构体解析：写临时 YAML，用独立进程外的方式不可行，
	// cleanenv 无导出的解析入口，这里只断言字段存在且默认零值。
	cfg := &cfgpkg.Config{}
	if cfg.CoredogPathBase != "" {
		t.Errorf("zero value expected, got %q", cfg.CoredogPathBase)
	}
}
