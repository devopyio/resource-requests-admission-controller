package main

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"io/ioutil"
	"net/http"
	"time"

	log "github.com/sirupsen/logrus"
	"k8s.io/api/admission/v1beta1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// Healthchecker checks admission controller health
type Healthchecker struct {
	port    string
	client  *http.Client
	reqBody []byte
}

var req = v1beta1.AdmissionReview{
	TypeMeta: v1.TypeMeta{
		Kind: "AdmissionReview",
	},
	Request: &v1beta1.AdmissionRequest{
		UID: "e911857d-c318-11e8-bbad-025000000001",
		Kind: v1.GroupVersionKind{
			Kind: "Pod",
		},
		Operation: "CREATE",
		Object: runtime.RawExtension{
			Raw: []byte(`{"metadata": {
        						"name": "test",
        						"uid": "e911857d-c318-11e8-bbad-025000000001",
						        "creationTimestamp": "2018-09-28T12:20:39Z"
      						}}`),
		},
	},
}

// NewHealhChecker creates New Healthchecker
func NewHealhChecker(port string) (*Healthchecker, error) {
	defaultTransport := http.DefaultTransport.(*http.Transport)

	// Create new Transport that ignores self-signed SSL
	transport := &http.Transport{
		Proxy:                 defaultTransport.Proxy,
		DialContext:           defaultTransport.DialContext,
		MaxIdleConns:          defaultTransport.MaxIdleConns,
		IdleConnTimeout:       defaultTransport.IdleConnTimeout,
		ExpectContinueTimeout: defaultTransport.ExpectContinueTimeout,
		TLSHandshakeTimeout:   defaultTransport.TLSHandshakeTimeout,
		TLSClientConfig:       &tls.Config{InsecureSkipVerify: true},
	}
	client := &http.Client{
		Transport: transport,
		Timeout:   time.Second * 10,
	}
	reqBody, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}

	return &Healthchecker{
		port:    port,
		client:  client,
		reqBody: reqBody,
	}, nil
}

// ServeHTTP serves HTTP request
func (hc *Healthchecker) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	resp, err := hc.client.Post("https://localhost:"+hc.port, "application/json", bytes.NewReader(hc.reqBody))
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_, err := w.Write([]byte(err.Error()))
		if err != nil {
			log.WithError(err).Warn("could not write error response")
		}
		return
	}
	defer resp.Body.Close()

	response, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_, err := w.Write([]byte(err.Error()))
		if err != nil {
			log.WithError(err).Warn("could not write error response")
		}

		return
	}

	review := &v1beta1.AdmissionReview{}
	_, _, err = codecs.UniversalDeserializer().Decode(response, nil, review)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_, err := w.Write([]byte(err.Error()))
		if err != nil {
			log.WithError(err).Warn("could not write error response")
		}

		return
	}

	if !review.Response.Allowed {
		w.WriteHeader(http.StatusInternalServerError)
		_, err := w.Write([]byte("error request not allowed"))
		if err != nil {
			log.WithError(err).Warn("could not write error response")
		}

		return
	}

	w.WriteHeader(http.StatusOK)
}
