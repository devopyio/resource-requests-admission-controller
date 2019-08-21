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
	appsv1 "k8s.io/api/apps/v1"
	v1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	batchv1beta1 "k8s.io/api/batch/v1beta1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
)

var (
	runtimeScheme = runtime.NewScheme()
	codecs        = serializer.NewCodecFactory(runtimeScheme)
	_             = codecs.UniversalDeserializer()
	// (https://github.com/kubernetes/kubernetes/issues/57982)
	_ = runtime.ObjectDefaulter(runtimeScheme)

	podIDRegex  = regexp.MustCompile("(.*)(-[0-9A-Za-z]+-[0-9A-Za-z]+)")
	podID2Regex = regexp.MustCompile("(.*)(-[0-9A-Za-z]+)")

	admissionCounter = promauto.NewCounterVec(prometheus.CounterOpts{Name: "admission_requests_total"}, []string{"allowed"})
	errorsCounter    = promauto.NewCounter(prometheus.CounterOpts{Name: "errors_total"})
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
	daemonsetKind   = "DaemonSet"
	podKind         = "Pod"
	jobKind         = "Job"
	cronJobKind     = "CronJob"
	pvcKind         = "PersistentVolumeClaim"
)

// Conf get configuration intercace
type Conf interface {
	GetPodLimit(nn NameNamespace) (cpu, mem *resource.Quantity, unlimited bool)
	GetMaxPVCSize(nn NameNamespace) (pvc *resource.Quantity, unlimited bool)
}

// ResourceRequestsAdmission handles admission based on resourcer returned by Conf
type ResourceRequestsAdmission struct {
	conf Conf
}

// New Creates new ResourceRequestsAdmission
func New(conf Conf) *ResourceRequestsAdmission {
	admissionCounter.WithLabelValues("true")
	admissionCounter.WithLabelValues("false")

	return &ResourceRequestsAdmission{
		conf: conf,
	}
}

// HandleAdmission handles admission request and denies if limits < resources requests
func (rra *ResourceRequestsAdmission) HandleAdmission(req *v1beta1.AdmissionRequest) (*v1beta1.AdmissionResponse, error) {
	resp, err := rra.handleAdmission(req)
	if err != nil {
		errorsCounter.Inc()
		log.WithError(err).Errorf("unable to handle request: %v", req)
		return resp, err
	}

	if resp.Allowed {
		admissionCounter.WithLabelValues("true").Inc()
	} else {
		admissionCounter.WithLabelValues("false").Inc()
	}

	return resp, nil
}

func (rra *ResourceRequestsAdmission) handleAdmission(req *v1beta1.AdmissionRequest) (*v1beta1.AdmissionResponse, error) {
	resp := &v1beta1.AdmissionResponse{
		UID:     req.UID,
		Allowed: true,
	}

	if req.Operation != v1beta1.Create && req.Operation != v1beta1.Update {
		return resp, nil
	}

	switch req.Kind.Kind {
	case podKind:
		var pod corev1.Pod
		if err := json.Unmarshal(req.Object.Raw, &pod); err != nil {
			return nil, errors.Wrapf(err, "unable to unmarshal json: %s", string(req.Object.Raw))
		}

		name := pod.Name
		match := podIDRegex.FindStringSubmatch(name)
		if len(match) == 3 {
			name = match[1]
		} else {
			match := podID2Regex.FindStringSubmatch(name)
			if len(match) == 3 {
				name = match[1]
			}
		}

		cpu, mem, unlimited := rra.conf.GetPodLimit(NameNamespace{
			Name:      name,
			Namespace: req.Namespace,
		})
		if unlimited {
			return resp, nil
		}

		if denyResp := rra.validatePodSpec(req, pod.Spec, cpu, mem); denyResp != nil {
			log.Infof("denying request for pod name: %s, namespace: %s, userInfo: %v", name, req.Namespace, req.UserInfo)
			return denyResp, nil
		}

		return resp, nil
	case deploymentKind:
		var deployment appsv1.Deployment
		if err := json.Unmarshal(req.Object.Raw, &deployment); err != nil {
			return nil, errors.Wrapf(err, "unable to unmarshal json: %s", string(req.Object.Raw))
		}

		cpu, mem, unlimited := rra.conf.GetPodLimit(NameNamespace{
			Name:      deployment.Name,
			Namespace: req.Namespace,
		})
		if unlimited {
			return resp, nil
		}

		if denyResp := rra.validatePodSpec(req, deployment.Spec.Template.Spec, cpu, mem); denyResp != nil {
			log.Infof("denying request for deployment name: %s, namespace: %s, userInfo: %v", deployment.Name, req.Namespace, req.UserInfo)
			return denyResp, nil
		}

		return resp, nil
	case statefulsetKind:
		var sts appsv1.StatefulSet
		if err := json.Unmarshal(req.Object.Raw, &sts); err != nil {
			return nil, errors.Wrapf(err, "unable to unmarshal json: %s", string(req.Object.Raw))
		}

		cpu, mem, unlimited := rra.conf.GetPodLimit(NameNamespace{
			Name:      sts.Name,
			Namespace: req.Namespace,
		})
		if unlimited {
			return resp, nil
		}

		if denyResp := rra.validatePodSpec(req, sts.Spec.Template.Spec, cpu, mem); denyResp != nil {
			log.Infof("denying request for statefulset name: %s, namespace: %s, userInfo: %v", sts.Name, req.Namespace, req.UserInfo)
			return denyResp, nil
		}

		return resp, nil
	case daemonsetKind:
		var ds appsv1.DaemonSet
		if err := json.Unmarshal(req.Object.Raw, &ds); err != nil {
			return nil, errors.Wrapf(err, "unable to unmarshal json: %s", string(req.Object.Raw))
		}

		cpu, mem, unlimited := rra.conf.GetPodLimit(NameNamespace{
			Name:      ds.Name,
			Namespace: req.Namespace,
		})
		if unlimited {
			return resp, nil
		}

		if denyResp := rra.validatePodSpec(req, ds.Spec.Template.Spec, cpu, mem); denyResp != nil {
			log.Infof("denying request for daemonset name: %s, namespace: %s, userInfo: %v", ds.Name, req.Namespace, req.UserInfo)
			return denyResp, nil
		}

		return resp, nil
	case cronJobKind:
		var cj batchv1beta1.CronJob
		if err := json.Unmarshal(req.Object.Raw, &cj); err != nil {
			return nil, errors.Wrapf(err, "unable to unmarshal json: %s", string(req.Object.Raw))
		}

		cpu, mem, unlimited := rra.conf.GetPodLimit(NameNamespace{
			Name:      cj.Name,
			Namespace: req.Namespace,
		})
		if unlimited {
			return resp, nil
		}

		if denyResp := rra.validatePodSpec(req, cj.Spec.JobTemplate.Spec.Template.Spec, cpu, mem); denyResp != nil {
			log.Infof("denying request for daemonset name: %s, namespace: %s, userInfo: %v", cj.Name, req.Namespace, req.UserInfo)
			return denyResp, nil
		}

		return resp, nil
	case jobKind:
		var j batchv1.Job
		if err := json.Unmarshal(req.Object.Raw, &j); err != nil {
			return nil, errors.Wrapf(err, "unable to unmarshal json: %s", string(req.Object.Raw))
		}

		cpu, mem, unlimited := rra.conf.GetPodLimit(NameNamespace{
			Name:      j.Name,
			Namespace: req.Namespace,
		})
		if unlimited {
			return resp, nil
		}

		if denyResp := rra.validatePodSpec(req, j.Spec.Template.Spec, cpu, mem); denyResp != nil {
			log.Infof("denying request for daemonset name: %s, namespace: %s, userInfo: %v", j.Name, req.Namespace, req.UserInfo)
			return denyResp, nil
		}

		return resp, nil
	case pvcKind:
		var pvc corev1.PersistentVolumeClaim
		if err := json.Unmarshal(req.Object.Raw, &pvc); err != nil {
			return nil, errors.Wrapf(err, "unable to unmarshal json: %s", string(req.Object.Raw))
		}

		maxSize, unlimited := rra.conf.GetMaxPVCSize(NameNamespace{
			Name:      pvc.Name,
			Namespace: req.Namespace,
		})
		if unlimited {
			return resp, nil
		}

		vSize, ok := pvc.Spec.Resources.Requests[corev1.ResourceStorage]
		if !ok {
			log.Infof("denying request for pvc name: %s, namespace: %s, userInfo: %v", pvc.Name, req.Namespace, req.UserInfo)
			return &v1beta1.AdmissionResponse{
				UID:     req.UID,
				Allowed: false,
				Result: &metav1.Status{
					Message: fmt.Sprintf("error persistentVolumeClaim %s size is empty", pvc.Name),
				},
			}, nil
		}

		if vSize.Cmp(*maxSize) > 0 {
			log.Infof("denying request for pvc name: %s, namespace: %s, userInfo: %v", pvc.Name, req.Namespace, req.UserInfo)
			return &v1beta1.AdmissionResponse{
				UID:     req.UID,
				Allowed: false,
				Result: &metav1.Status{
					Message: fmt.Sprintf("error persistentVolumeClaim %s size is %s > %s", pvc.Name, vSize.String(), maxSize.String()),
				},
			}, nil
		}
	}

	return resp, nil
}

func (rra *ResourceRequestsAdmission) validatePodSpec(req *v1beta1.AdmissionRequest, podSpec corev1.PodSpec, cpuLimit, memLimit *resource.Quantity) *v1beta1.AdmissionResponse {
	for _, container := range podSpec.Containers {
		if _, ok := container.Resources.Requests[corev1.ResourceCPU]; !ok {
			return &v1beta1.AdmissionResponse{
				UID:     req.UID,
				Allowed: false,
				Result: &metav1.Status{
					Message: fmt.Sprintf("error container %s requests.CPU is empty, must be 0", container.Name),
				},
			}
		}
		if _, ok := container.Resources.Requests[corev1.ResourceMemory]; !ok {
			return &v1beta1.AdmissionResponse{
				UID:     req.UID,
				Allowed: false,
				Result: &metav1.Status{
					Message: fmt.Sprintf("error container %s requests.Memory is empty, must be 0", container.Name),
				},
			}
		}

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
					Message: fmt.Sprintf("error container %s requests.Memory: %s > 0", container.Name, container.Resources.Requests.Memory()),
				},
			}
		}

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
					Message: fmt.Sprintf("error container %s limits.Memory: %s > %s", container.Name, container.Resources.Limits.Memory(), memLimit),
				},
			}
		}
	}

	return nil
}
