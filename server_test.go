package main

import (
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"k8s.io/api/admission/v1beta1"
	"k8s.io/apimachinery/pkg/api/resource"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

var (
	AdmissionRequestPod = v1beta1.AdmissionReview{
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
	AdmissionRequestPodDisallow = v1beta1.AdmissionReview{
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
)

func decodeResponse(t *testing.T, body io.ReadCloser) *v1beta1.AdmissionReview {
	response, err := ioutil.ReadAll(body)
	if err != nil {
		t.Fatal(err)
	}
	review := &v1beta1.AdmissionReview{}
	_, _, err = codecs.UniversalDeserializer().Decode(response, nil, review)
	if err != nil {
		t.Fatal(err)
	}
	return review
}

func encodeRequest(t *testing.T, review *v1beta1.AdmissionReview) []byte {
	ret, err := json.Marshal(review)
	if err != nil {
		t.Fatal(err)
	}
	return ret
}

func TestServeReturnsCorrectJson(t *testing.T) {
	conf := &MockConfiger{}
	rra := &ResourceRequestsAdmission{conf}
	server := httptest.NewServer(&AdmissionControllerServer{
		AdmissionController: rra,
		Decoder:             codecs.UniversalDeserializer(),
	})

	requestString := string(encodeRequest(t, &AdmissionRequestPod))
	myr := strings.NewReader(requestString)
	r, err := http.Post(server.URL, "application/json", myr)
	if err != nil {
		t.Fatal(err)
	}

	review := decodeResponse(t, r.Body)

	assert.Equal(t, review.Response.UID, AdmissionRequestPod.Request.UID)
	assert.Equal(t, review.Response.Allowed, true)
}

/*
func TestServeReturnsCorrectJson(t *testing.T) {
	rra := &ResourceRequestsAdmission{}
	server := httptest.NewServer(&AdmissionControllerServer{
		AdmissionController: rra,
		Decoder:             codecs.UniversalDeserializer(),
	})

	requestString := string(encodeRequest(t, &AdmissionRequestNS))
	myr := strings.NewReader(requestString)
	r, err := http.Post(server.URL, "application/json", myr)
	if err != nil {
		t.Fatal(err)
	}

	review := decodeResponse(t, r.Body)

	assert.Equal(t, review.Request.UID, AdmissionRequestNS.Request.UID)
	assert.Equal(t, review.Response.Allowed, true)
}
*/

type MockConfiger struct {
	cpu *resource.Quantity
	mem *resource.Quantity
}

func (c *MockConfiger) IsExcluded(nn NameNamespace) bool {
	return false
}

func (c *MockConfiger) GetResourceLimits() (cpu *resource.Quantity, mem *resource.Quantity) {
	return cpu, mem
}

func (c *MockConfiger) GetMaxPVCSize() *resource.Quantity {
	return nil
}

func TestCompareMemoryQuantity(t *testing.T) {
	q1 := resource.MustParse("1Gi")
	q2 := resource.MustParse("2147483648")

	fmt.Println(q1.Value())
	fmt.Println(q2.Value())

	fmt.Printf("%v, %v", q1, q2)
	assert.True(t, q1.Cmp(q2) < 0)
}

func TestCompareCPUQuantity(t *testing.T) {
	q1 := resource.MustParse("100m")
	q2 := resource.MustParse("0.5")

	fmt.Println(q1.MilliValue())
	fmt.Println(q2.MilliValue())

	fmt.Printf("%v, %v", q1, q2)
	assert.True(t, q1.Cmp(q2) < 0)
}
