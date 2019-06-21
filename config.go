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

// NameNamespace name + namespace combination, strings might be empty
type NameNamespace struct {
	Name      string `json:"name" yaml:"name"`
	Namespace string `json:"namespace" yaml:"namespace"`
}

// String nicely formats name and namespace
func (nn NameNamespace) String() string {
	return "Name: " + nn.Name + ", " + nn.Namespace
}

// Limit describes limit configuration in yaml
type Limit struct {
	CPULimit  string `yaml:"maxCPULimit" json:"maxCPULimit"`
	MemLimit  string `yaml:"maxMemLimit" json:"maxMemLimit"`
	PVCSize   string `yaml:"maxPVCSize" json:"maxPVCSize"`
	Unlimited bool   `yaml:"unlimited" json:"unlimited"`
}

// Config describes Config files structure
type Config struct {
	Namespaces  map[string]Limit        `yaml:"customNamespaces" json:"namespaces"`
	Names       map[NameNamespace]Limit `yaml:"customNames" json:"names"`
	MaxCPULimit string                  `yaml:"maxCPULimit" json:"maxCPULimit"`
	MaxMemLimit string                  `yaml:"maxMemLimit" json:"maxMemLimit"`
	MaxPvcSize  string                  `yaml:"maxPVCSize" json:"maxPVCSize"`
}

// LimitResource resource limits
type LimitResource struct {
	CPULimit  *resource.Quantity
	MemLimit  *resource.Quantity
	PVCSize   *resource.Quantity
	Unlimited bool
}

// Configurer configures resource limits
type Configurer struct {
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

// NewConfigurer returns new Limits Configurer
func NewConfigurer(filePath string, refreshInterval time.Duration) (*Configurer, error) {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	if err := w.Add(filePath); err != nil {
		return nil, err
	}

	c := &Configurer{
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

func (c *Configurer) convertLimitsToResources(limit Limit) (*LimitResource, error) {
	var cpu, mem, pvc *resource.Quantity
	switch {
	case limit.CPULimit != "":
		q, err := resource.ParseQuantity(limit.CPULimit)
		if err != nil {
			return nil, errors.Wrap(err, "could not parse CPULimit")
		}
		cpu = &q
	case c.maxCPULimit != nil:
		q := c.maxCPULimit.DeepCopy()
		cpu = &q
	}

	switch {
	case limit.MemLimit != "":
		q, err := resource.ParseQuantity(limit.MemLimit)
		if err != nil {
			return nil, errors.Wrap(err, "could not parse MemLimit")
		}

		mem = &q
	case c.maxMemLimit != nil:
		q := c.maxMemLimit.DeepCopy()
		mem = &q
	}

	switch {
	case limit.PVCSize != "":
		q, err := resource.ParseQuantity(limit.PVCSize)
		if err != nil {
			return nil, errors.Wrap(err, "could not parse PVCSize")
		}
		pvc = &q
	case c.maxPvcSize != nil:
		q := c.maxPvcSize.DeepCopy()
		pvc = &q
	}

	return &LimitResource{
		CPULimit:  cpu,
		MemLimit:  mem,
		PVCSize:   pvc,
		Unlimited: limit.Unlimited,
	}, nil
}

// load loads configuration
func (c *Configurer) load() error {
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
		rLimit, err := c.convertLimitsToResources(limit)
		if err != nil {
			return errors.Wrapf(err, "namespace: %s", ns)
		}

		c.excludedNamespaces[ns] = *rLimit
	}

	for nn, limit := range config.Names {
		rLimit, err := c.convertLimitsToResources(limit)
		if err != nil {
			return errors.Wrapf(err, "nn: %s", nn)
		}

		c.excludedNames[nn] = *rLimit
	}

	log.Debugf("exluding namespaces: %v, names: %v, maxCPULimit: %v, maxMemLimit: %v, maxPvcSize: %v", config.Namespaces, config.Names, c.maxCPULimit, c.maxMemLimit, c.maxPvcSize)
	return nil
}

// GetPodLimit gets pod CPU and memory limit from configmap.
func (c *Configurer) GetPodLimit(nn NameNamespace) (cpu, mem *resource.Quantity, unlimited bool) {
	c.m.RLock()
	defer c.m.RUnlock()

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
	if c.maxCPULimit != nil {
		q := c.maxCPULimit.DeepCopy()
		cpu = &q
	}
	if c.maxMemLimit != nil {
		q := c.maxMemLimit.DeepCopy()
		mem = &q
	}

	return cpu, mem, false
}

// GetMaxPVCSize returns PVC limit, might return nil if both maxPvcSize and custom pvc size is not set
func (c *Configurer) GetMaxPVCSize(nn NameNamespace) (pvc *resource.Quantity, unlimited bool) {
	c.m.RLock()
	defer c.m.RUnlock()

	if limit, ok := c.excludedNames[nn]; ok {
		if limit.Unlimited {
			return nil, true
		}

		if limit.PVCSize != nil {
			q := limit.PVCSize.DeepCopy()
			pvc = &q
		}

		return pvc, false
	}

	if limit, ok := c.excludedNamespaces[nn.Namespace]; ok {
		if limit.Unlimited {
			return nil, true
		}

		if limit.PVCSize != nil {
			q := limit.PVCSize.DeepCopy()
			pvc = &q
		}
		return pvc, false
	}
	if c.maxPvcSize != nil {
		q := c.maxPvcSize.DeepCopy()
		pvc = &q
	}

	return pvc, false
}

// Watch starts the watching of filepath changes and reloads configuration.
func (c *Configurer) Watch() {
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
			if err != nil {
				log.WithError(err).Error("watch error")
			}
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

// Close stop the inotify watching
func (c *Configurer) Close() error {
	return c.w.Close()
}
