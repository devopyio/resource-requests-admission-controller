package main

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestConfigGetKubeSystem(t *testing.T) {
	configFile := "./testdata/test.yaml"
	configer, err := NewConfigurer(configFile, 1*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	defer configer.Close()

	pvc, unlimited := configer.GetMaxPVCSize(NameNamespace{
		Name:      "",
		Namespace: "kube-system",
	})

	assert.Equal(t, false, unlimited)
	//Value is in bytes, expected Limit 50Gb
	assert.Equal(t, int64(50*1024*1024*1024), pvc.Value())

	cpu, mem, unlimited := configer.GetPodLimit(NameNamespace{
		Name:      "",
		Namespace: "kube-system",
	})

	assert.Equal(t, false, unlimited)
	assert.Equal(t, int64(1), cpu.Value())
	assert.Equal(t, int64(2*1024*1024*1024), mem.Value())
}

func TestConfigGetMonitoring(t *testing.T) {
	configFile := "./testdata/test.yaml"
	configer, err := NewConfigurer(configFile, 1*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	defer configer.Close()

	pvc, unlimited := configer.GetMaxPVCSize(NameNamespace{
		Name:      "",
		Namespace: "monitoring",
	})

	assert.Equal(t, false, unlimited)
	//Value is in bytes, expected Limit 50Gb
	assert.Equal(t, int64(50*1024*1024*1024), pvc.Value())

	cpu, mem, unlimited := configer.GetPodLimit(NameNamespace{
		Name:      "",
		Namespace: "monitoring",
	})

	assert.Equal(t, false, unlimited)
	assert.Equal(t, int64(2), cpu.Value())
	assert.Equal(t, int64(1*1024*1024*1024), mem.Value())
}

func TestConfigGetDefault(t *testing.T) {
	configFile := "./testdata/test.yaml"
	configer, err := NewConfigurer(configFile, 1*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	defer configer.Close()

	pvc, unlimited := configer.GetMaxPVCSize(NameNamespace{
		Name:      "",
		Namespace: "default",
	})

	assert.Equal(t, true, unlimited)
	assert.Nil(t, pvc)

	cpu, mem, unlimited := configer.GetPodLimit(NameNamespace{
		Name:      "",
		Namespace: "default",
	})

	assert.Equal(t, true, unlimited)
	assert.Nil(t, cpu)
	assert.Nil(t, mem)
}

func TestConfigGetTestNamespace(t *testing.T) {
	configFile := "./testdata/test.yaml"
	configer, err := NewConfigurer(configFile, 1*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	defer configer.Close()

	pvc, unlimited := configer.GetMaxPVCSize(NameNamespace{
		Name:      "",
		Namespace: "test-namespace",
	})

	assert.Equal(t, false, unlimited)
	assert.Equal(t, int64(10*1024*1024*1024), pvc.Value())

	cpu, mem, unlimited := configer.GetPodLimit(NameNamespace{
		Name:      "",
		Namespace: "test-namespace",
	})

	assert.Equal(t, false, unlimited)
	assert.Equal(t, int64(1), cpu.Value())
	assert.Equal(t, int64(1*1024*1024*1024), mem.Value())
}

func TestConfigGetTestPod(t *testing.T) {
	configFile := "./testdata/test.yaml"
	configer, err := NewConfigurer(configFile, 1*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	defer configer.Close()

	pvc, unlimited := configer.GetMaxPVCSize(NameNamespace{
		Name:      "deployment-name",
		Namespace: "test-namespace",
	})

	assert.Equal(t, false, unlimited)
	assert.Equal(t, int64(15*1024*1024*1024), pvc.Value())

	cpu, mem, unlimited := configer.GetPodLimit(NameNamespace{
		Name:      "deployment-name",
		Namespace: "test-namespace",
	})

	assert.Equal(t, false, unlimited)
	assert.Equal(t, int64(2), cpu.Value())
	assert.Equal(t, int64(5*1024*1024*1024), mem.Value())
}
