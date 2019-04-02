package main

import (
	"io/ioutil"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/pkg/errors"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	log "github.com/sirupsen/logrus"
	yaml "gopkg.in/yaml.v2"
	"k8s.io/apimachinery/pkg/api/resource"
)

var (
	reloadCounter       = promauto.NewCounter(prometheus.CounterOpts{Name: "reload_total"})
	reloadErrorsCounter = promauto.NewCounter(prometheus.CounterOpts{Name: "reload_errors_total"})
)

type NameNamespace struct {
	Name      string `json:"name" yaml:"name"`
	Namespace string `json:"namespace" yaml:"namespace"`
}

func (nn NameNamespace) String() string {
	return "Name: " + nn.Name + ", " + nn.Namespace
}

type Limit struct {
	CPULimit  string `yaml:"cpuLimit" json:"cpuLimit"`
	MemLimit  string `yaml:"memLimit" json:"memLimit"`
	PvcLimit  string `yaml:"pvcLimit" json:"pvcLimit"`
	Unlimited bool   `yaml:"unlimited" json:"unlimited"`
}

// Config describes Config files structure
type Config struct {
	Namespaces  map[string]Limit        `yaml:"excludedNamespaces" json:"namespaces"`
	Names       map[NameNamespace]Limit `yaml:"excludedNames" json:"names"`
	MaxCPULimit string                  `yaml:"maxCPULimit" json:"maxCPULimit"`
	MaxMemLimit string                  `yaml:"maxMemLimit" json:"maxMemLimit"`
	MaxPvcSize  string                  `yaml:"maxPvcSize" json:"maxPvcSize"`
}

type LimitResource struct {
	CPULimit  *resource.Quantity
	MemLimit  *resource.Quantity
	PVCLimit  *resource.Quantity
	Unlimited bool
}

// Config
type Configer struct {
	filePath        string
	refreshInterval time.Duration
	w               *fsnotify.Watcher

	excludedNames      map[NameNamespace]LimitResource
	excludedNamespaces map[string]LimitResource
	maxCPULimit        *resource.Quantity
	maxMemLimit        *resource.Quantity
	maxPvcSize         *resource.Quantity
	m                  sync.RWMutex
}

// NewConfiger returns new Limits Configurer
func NewConfiger(filePath string, refreshInterval time.Duration) (*Configer, error) {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	if err := w.Add(filePath); err != nil {
		return nil, err
	}

	c := &Configer{
		filePath:           filePath,
		w:                  w,
		refreshInterval:    refreshInterval,
		excludedNamespaces: nil,
		excludedNames:      nil,
	}

	if err := c.load(); err != nil {
		return nil, err
	}

	go c.Watch()

	return c, nil
}

func (c *Configer) load() error {
	configFile, err := ioutil.ReadFile(c.filePath)
	if err != nil {
		return errors.Wrap(err, "unable to read file")
	}

	var config Config
	if err := yaml.Unmarshal(configFile, &config); err != nil {
		return errors.Wrap(err, "unable to unmarshal yaml file")
	}

	c.m.Lock()
	defer c.m.Unlock()

	if config.MaxCPULimit != "" {
		q, err := resource.ParseQuantity(config.MaxCPULimit)
		if err != nil {
			return errors.Wrap(err, "could not parse MaxCPULimit")
		}
		c.maxCPULimit = &q
	}

	if config.MaxMemLimit != "" {
		q, err := resource.ParseQuantity(config.MaxMemLimit)
		if err != nil {
			return errors.Wrap(err, "could not parse MaxMemLimit")
		}

		c.maxMemLimit = &q
	}
	if config.MaxPvcSize != "" {
		q, err := resource.ParseQuantity(config.MaxPvcSize)
		if err != nil {
			return errors.Wrap(err, "could not parse MaxPvcSize")
		}

		c.maxPvcSize = &q
	}

	c.excludedNamespaces = make(map[string]LimitResource)
	c.excludedNames = make(map[NameNamespace]LimitResource)
	for ns, limit := range config.Namespaces {
		var (
			cpu *resource.Quantity
			mem *resource.Quantity
			pvc *resource.Quantity
		)
		if limit.CPULimit != "" {
			q, err := resource.ParseQuantity(limit.CPULimit)
			if err != nil {
				return errors.Wrapf(err, "could not parse CPULimit for ns %s ", ns)
			}
			cpu = &q
		}

		if limit.MemLimit != "" {
			q, err := resource.ParseQuantity(limit.MemLimit)
			if err != nil {
				return errors.Wrapf(err, "could not parse MemLimit for ns %s ", ns)
			}

			mem = &q
		}

		if limit.PvcLimit != "" {
			q, err := resource.ParseQuantity(limit.CPULimit)
			if err != nil {
				return errors.Wrapf(err, "could not parse CPULimit for ns %s ", ns)
			}
			pvc = &q
		}

		c.excludedNamespaces[ns] = LimitResource{
			CPULimit:  cpu,
			MemLimit:  mem,
			PVCLimit:  pvc,
			Unlimited: limit.Unlimited,
		}
	}

	for nn, limit := range config.Names {
		var (
			cpu *resource.Quantity
			mem *resource.Quantity
			pvc *resource.Quantity
		)
		if limit.CPULimit != "" {
			q, err := resource.ParseQuantity(limit.CPULimit)
			if err != nil {
				return errors.Wrapf(err, "could not parse CPULimit for nn: %s", nn)
			}
			cpu = &q
		}

		if limit.MemLimit != "" {
			q, err := resource.ParseQuantity(limit.MemLimit)
			if err != nil {
				return errors.Wrapf(err, "could not parse MemLimit for nn: %s", nn)
			}

			mem = &q
		}

		if limit.PvcLimit != "" {
			q, err := resource.ParseQuantity(limit.CPULimit)
			if err != nil {
				return errors.Wrapf(err, "could not parse CPULimit for nn: %s", nn)
			}
			pvc = &q
		}
		c.excludedNames[nn] = LimitResource{
			CPULimit:  cpu,
			MemLimit:  mem,
			PVCLimit:  pvc,
			Unlimited: limit.Unlimited,
		}

	}

	log.Infof("exluding namespaces: %v, names: %v, maxCPULimit: %v, maxMemLimit: %v, maxPvcSize: %v", config.Namespaces, config.Names, c.maxCPULimit, c.maxMemLimit, c.maxPvcSize)
	return nil
}

// GetPodLimit gets pod CPU and memory limit from configmap.
func (c *Configer) GetPodLimit(nn NameNamespace) (cpu, mem *resource.Quantity, unlimited bool) {
	c.m.RLock()
	defer c.m.RUnlock()
	if limit, ok := c.excludedNamespaces[nn.Namespace]; ok {
		if limit.Unlimited {
			return nil, nil, true
		}

		if limit.CPULimit != nil {
			q := limit.CPULimit.DeepCopy()
			cpu = &q
		}
		if limit.MemLimit != nil {
			q := limit.MemLimit.DeepCopy()
			mem = &q
		}
		return cpu, mem, false
	}
	if limit, ok := c.excludedNames[nn]; ok {
		if limit.Unlimited {
			return nil, nil, true
		}

		if limit.CPULimit != nil {
			q := limit.CPULimit.DeepCopy()
			cpu = &q
		}
		if limit.MemLimit != nil {
			q := limit.MemLimit.DeepCopy()
			mem = &q
		}
		return cpu, mem, false
	}

	if c.maxCPULimit != nil {
		cpuCopy := c.maxCPULimit.DeepCopy()
		cpu = &cpuCopy
	}
	if c.maxMemLimit != nil {
		memCopy := c.maxMemLimit.DeepCopy()
		mem = &memCopy
	}

	return c.maxCPULimit, c.maxMemLimit, false
}

// GetMaxPVCSize returns PVC limit
func (c *Configer) GetMaxPVCSize(nn NameNamespace) (pvc *resource.Quantity, unlimited bool) {
	c.m.RLock()
	defer c.m.RUnlock()

	if limit, ok := c.excludedNamespaces[nn.Namespace]; ok {
		if limit.Unlimited {
			return nil, true
		}

		if limit.PVCLimit != nil {
			q := limit.PVCLimit.DeepCopy()
			pvc = &q
		}
		return pvc, false
	}
	if limit, ok := c.excludedNames[nn]; ok {
		if limit.Unlimited {
			return nil, true
		}

		if limit.PVCLimit != nil {
			q := limit.PVCLimit.DeepCopy()
			pvc = &q
		}
		return pvc, false
	}
	if c.maxPvcSize != nil {
		pvcCopy := c.maxPvcSize.DeepCopy()
		pvc = &pvcCopy
	}

	return pvc, false
}

func (c *Configer) Watch() {
	tick := time.NewTicker(c.refreshInterval)
	defer tick.Stop()

	for {
		select {
		case <-tick.C:
		case event := <-c.w.Events:
			if event.Name != c.filePath {
				continue
			}
		case err := <-c.w.Errors:
			log.WithError(err).Error("watch error")
			continue
		}

		err := c.load()
		if err != nil {
			reloadErrorsCounter.Inc()
			log.WithError(err).Error("config load error")
		}

		reloadCounter.Inc()
	}
}

func (c *Configer) Close() error {
	return c.w.Close()
}
