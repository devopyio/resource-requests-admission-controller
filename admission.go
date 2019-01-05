package main

import (
	"encoding/json"
	"fmt"
	"regexp"

	"github.com/pkg/errors"
	"k8s.io/api/admission/v1beta1"
	admissionregistrationv1beta1 "k8s.io/api/admissionregistration/v1beta1"
	v1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"

	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
)

var (
	runtimeScheme = runtime.NewScheme()
	codecs        = serializer.NewCodecFactory(runtimeScheme)
	deserializer  = codecs.UniversalDeserializer()
	// (https://github.com/kubernetes/kubernetes/issues/57982)
	defaulter = runtime.ObjectDefaulter(runtimeScheme)

	podIdRegex  = regexp.MustCompile("(.*)(-[0-9A-Za-z]{10}-[0-9A-Za-z]{5})")
	podId2Regex = regexp.MustCompile("(.*)(-[0-9]+)")
)

func init() {
	_ = corev1.AddToScheme(runtimeScheme)
	_ = admissionregistrationv1beta1.AddToScheme(runtimeScheme)
	// defaulting with webhooks:
	// https://github.com/kubernetes/kubernetes/issues/57982
	_ = v1.AddToScheme(runtimeScheme)
}

const (
	deploymentKind  = "Deployment"
	statefulsetKind = "Statefulset"
	podKind         = "Pod"
)

type NameNamespace struct {
	Name      string
	Namespace string
}

type ResourceRequestsAdmission struct {
	ExcludedNames      map[NameNamespace]struct{}
	ExcludedNamespaces map[string]struct{}
}

func (rra *ResourceRequestsAdmission) HandleAdmission(req *v1beta1.AdmissionRequest) (*v1beta1.AdmissionResponse, error) {
	resp := &v1beta1.AdmissionResponse{
		UID:     req.UID,
		Allowed: true,
	}
	kind := req.Kind.Kind
	if kind != deploymentKind && kind != statefulsetKind && kind != podKind {
		return resp, nil
	}

	if req.Operation != v1beta1.Create && req.Operation != v1beta1.Update {
		return resp, nil
	}

	name := req.Name

	if kind == podKind {
		match := podIdRegex.FindStringSubmatch(name)
		if len(match) == 3 {
			name = match[1]
		} else {
			match := podId2Regex.FindStringSubmatch(name)
			if len(match) == 3 {
				name = match[1]
			}
		}
	}
	if _, ok := rra.ExcludedNamespaces[req.Namespace]; ok {
		return resp, nil
	}

	if _, ok := rra.ExcludedNames[NameNamespace{
		Name:      name,
		Namespace: req.Namespace,
	}]; ok {
		return resp, nil
	}

	switch kind {
	case podKind:
		var pod corev1.Pod
		if err := json.Unmarshal(req.Object.Raw, &pod); err != nil {
			return nil, errors.Wrapf(err, "unable to unmarshal json: %s", string(req.Object.Raw))
		}

		if denyResp := validatePodSpec(req, pod.Spec); denyResp != nil {
			return denyResp, nil
		}
	case deploymentKind:
		//TODO: handle conversion
		var deployment appsv1.Deployment
		if err := json.Unmarshal(req.Object.Raw, &deployment); err != nil {
			return nil, errors.Wrapf(err, "unable to unmarshal json: %s", string(req.Object.Raw))
		}

		if denyResp := validatePodSpec(req, deployment.Spec.Template.Spec); denyResp != nil {
			return denyResp, nil
		}
	case statefulsetKind:
		var sts appsv1.StatefulSet
		if err := json.Unmarshal(req.Object.Raw, &sts); err != nil {
			return nil, errors.Wrapf(err, "unable to unmarshal json: %s", string(req.Object.Raw))
		}

		if denyResp := validatePodSpec(req, sts.Spec.Template.Spec); denyResp != nil {
			return denyResp, nil
		}
	}

	return resp, nil
}

func validatePodSpec(req *v1beta1.AdmissionRequest, podSpec corev1.PodSpec) *v1beta1.AdmissionResponse {
	for _, container := range podSpec.Containers {
		if container.Resources.Requests.Cpu().CmpInt64(0) > 0 {
			return &v1beta1.AdmissionResponse{
				UID:     req.UID,
				Allowed: false,
				Result: &metav1.Status{
					Message: fmt.Sprintf("Error container %s requests.CPU: %s > 0", container.Name, container.Resources.Requests.Cpu()),
				},
			}
		}

		if container.Resources.Requests.Memory().CmpInt64(0) > 0 {
			return &v1beta1.AdmissionResponse{
				UID:     req.UID,
				Allowed: false,
				Result: &metav1.Status{
					Message: fmt.Sprintf("Error container %s requests.Mem: %s > 0", container.Name, container.Resources.Requests.Cpu()),
				},
			}
		}
	}

	return nil
}
