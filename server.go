package main

import (
	"io/ioutil"

	"net/http"

	log "github.com/sirupsen/logrus"
	"k8s.io/api/admission/v1beta1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/json"
)

type AdmissionController interface {
	HandleAdmission(review *v1beta1.AdmissionRequest) (*v1beta1.AdmissionResponse, error)
}

type AdmissionControllerServer struct {
	AdmissionController AdmissionController
	Decoder             runtime.Decoder
}

func (acs *AdmissionControllerServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var body []byte
	if data, err := ioutil.ReadAll(r.Body); err == nil {
		body = data
	}
	log.WithField("req", string(body)).Debug("handling request")

	review := &v1beta1.AdmissionReview{}

	_, _, err := acs.Decoder.Decode(body, nil, review)
	if err != nil {
		log.WithError(err).Error("unable to decode request")
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	resp, err := acs.AdmissionController.HandleAdmission(review.Request)
	if err != nil {
		log.WithError(err).Error("unable to handle admission request")
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}

	review.Response = resp
	responseInBytes, err := json.Marshal(review)
	if err != nil {
		log.WithError(err).Error("unable to marshal response")
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}

	log.WithField("resp", string(body)).Debug("handling response")

	if _, err := w.Write(responseInBytes); err != nil {
		log.WithError(err).Error("unable to write response")
		return
	}
}
