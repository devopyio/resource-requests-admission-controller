package main

import (
	"encoding/json"
	"fmt"
	"regexp"

	"github.com/pkg/errors"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	log "github.com/sirupsen/logrus"
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
	daemonset       = "Daemonset"
	podKind         = "Pod"
)

var (
	admissionCounter = promauto.NewCounterVec(prometheus.CounterOpts{Name: "admission_requests_total"}, []string{"allowed"})
	errorsCounter    = promauto.NewCounter(prometheus.CounterOpts{Name: "errors_total"})
)

type ResourceRequestsAdmission struct {
	conf *Configer
}

func New(conf *Configer) *ResourceRequestsAdmission {
	admissionCounter.WithLabelValues("true")
	admissionCounter.WithLabelValues("false")

	return &ResourceRequestsAdmission{
		conf: conf,
	}
}

func (rra *ResourceRequestsAdmission) HandleAdmission(req *v1beta1.AdmissionRequest) (*v1beta1.AdmissionResponse, error) {
	resp, err := rra.handleAdmission(req)
	if err != nil {
		errorsCounter.Inc()
		log.WithError(err).Error("unable to handle request: %v", req)
		return resp, err
	}

	if resp.Allowed {
		admissionCounter.WithLabelValues("true").Inc()
	} else {
		log.Infof("denying request for name: %s, namespace: %s, userInfo: %v", req.Name, req.Namespace, req.UserInfo)
		admissionCounter.WithLabelValues("false").Inc()
	}

	return resp, nil
}

func (rra *ResourceRequestsAdmission) handleAdmission(req *v1beta1.AdmissionRequest) (*v1beta1.AdmissionResponse, error) {
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

	if ok := rra.conf.IsExcluded(NameNamespace{
		Name:      name,
		Namespace: req.Namespace,
	}); ok {
		return resp, nil
	}

	switch kind {
	case podKind:
		var pod corev1.Pod
		if err := json.Unmarshal(req.Object.Raw, &pod); err != nil {
			return nil, errors.Wrapf(err, "unable to unmarshal json: %s", string(req.Object.Raw))
		}

		if denyResp := rra.validatePodSpec(req, pod.Spec); denyResp != nil {
			return denyResp, nil
		}
	case deploymentKind:
		//TODO: handle conversion
		var deployment appsv1.Deployment
		if err := json.Unmarshal(req.Object.Raw, &deployment); err != nil {
			return nil, errors.Wrapf(err, "unable to unmarshal json: %s", string(req.Object.Raw))
		}

		if denyResp := rra.validatePodSpec(req, deployment.Spec.Template.Spec); denyResp != nil {
			return denyResp, nil
		}
	case statefulsetKind:
		var sts appsv1.StatefulSet
		if err := json.Unmarshal(req.Object.Raw, &sts); err != nil {
			return nil, errors.Wrapf(err, "unable to unmarshal json: %s", string(req.Object.Raw))
		}

		if denyResp := rra.validatePodSpec(req, sts.Spec.Template.Spec); denyResp != nil {
			return denyResp, nil
		}
	case daemonset:
		var ds appsv1.DaemonSet
		if err := json.Unmarshal(req.Object.Raw, &ds); err != nil {
			return nil, errors.Wrapf(err, "unable to unmarshal json: %s", string(req.Object.Raw))
		}

		if denyResp := rra.validatePodSpec(req, ds.Spec.Template.Spec); denyResp != nil {
			return denyResp, nil
		}

	}

	return resp, nil
}

func (rra *ResourceRequestsAdmission) validatePodSpec(req *v1beta1.AdmissionRequest, podSpec corev1.PodSpec) *v1beta1.AdmissionResponse {
	for _, container := range podSpec.Containers {
		if container.Resources.Requests.Cpu().CmpInt64(0) > 0 {
			return &v1beta1.AdmissionResponse{
				UID:     req.UID,
				Allowed: false,
				Result: &metav1.Status{
					Message: fmt.Sprintf("error container %s requests.CPU: %s > 0", container.Name, container.Resources.Requests.Cpu()),
				},
			}
		}

		if container.Resources.Requests.Memory().CmpInt64(0) > 0 {
			return &v1beta1.AdmissionResponse{
				UID:     req.UID,
				Allowed: false,
				Result: &metav1.Status{
					Message: fmt.Sprintf("error container %s requests.Mem: %s > 0", container.Name, container.Resources.Requests.Cpu()),
				},
			}
		}

		cpuLimit, memLimit := rra.conf.GetResourceLimits()
		if cpuLimit != nil && container.Resources.Limits.Cpu().Cmp(*cpuLimit) > 0 {
			return &v1beta1.AdmissionResponse{
				UID:     req.UID,
				Allowed: false,
				Result: &metav1.Status{
					Message: fmt.Sprintf("error container %s limits.CPU: %s > %s", container.Name, container.Resources.Limits.Cpu(), cpuLimit),
				},
			}
		}

		if memLimit != nil && container.Resources.Limits.Memory().Cmp(*memLimit) > 0 {
			return &v1beta1.AdmissionResponse{
				UID:     req.UID,
				Allowed: false,
				Result: &metav1.Status{
					Message: fmt.Sprintf("error container %s requests.MemoryLimit: %s > %s", container.Name, container.Resources.Limits.Memory(), memLimit),
				},
			}
		}
	}

	return nil
}
