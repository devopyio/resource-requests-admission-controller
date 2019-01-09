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

//Config describes Config files structure
type Config struct {
	Namespaces  []string        `yaml:"excludedNamespaces" json:"namespaces"`
	Names       []NameNamespace `yaml:"excludedNames" json:"names"`
	MaxCPULimit string          `yaml:"maxCPULimit" json:"maxCPULimit"`
	MaxMemLimit string          `yaml:"maxMemLimit" json:"maxMemLimit"`
	MaxPvcSize  string          `yaml:"maxPvcSize" json:"maxPvcSize"`
}

type Configer struct {
	filePath        string
	refreshInterval time.Duration
	w               *fsnotify.Watcher

	excludedNames      map[NameNamespace]struct{}
	excludedNamespaces map[string]struct{}
	maxCPULimit        *resource.Quantity
	maxMemLimit        *resource.Quantity
	maxPvcSize         *resource.Quantity
	m                  sync.RWMutex
}

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
		excludedNamespaces: make(map[string]struct{}),
		excludedNames:      make(map[NameNamespace]struct{}),
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

	c.excludedNamespaces = make(map[string]struct{})
	c.excludedNames = make(map[NameNamespace]struct{})
	for _, ns := range config.Namespaces {
		c.excludedNamespaces[ns] = struct{}{}
	}

	for _, nn := range config.Names {
		c.excludedNames[nn] = struct{}{}
	}

	log.Infof("exluding namespaces: %q, names: %q, maxCPULimit: %v, maxMemLimit: %v, maxPvcSize: %v", config.Namespaces, config.Names, c.maxCPULimit, c.maxMemLimit, c.maxPvcSize)
	return nil
}

func (c *Configer) IsExcluded(nn NameNamespace) bool {
	c.m.RLock()
	defer c.m.RUnlock()
	if _, ok := c.excludedNamespaces[nn.Namespace]; ok {
		return true
	}
	if _, ok := c.excludedNames[nn]; ok {
		return true
	}

	return false
}

// GetResourceLimits returns resource limits
func (c *Configer) GetResourceLimits() (cpu *resource.Quantity, mem *resource.Quantity) {
	c.m.RLock()
	defer c.m.RUnlock()

	if c.maxCPULimit != nil {
		cpuCopy := c.maxCPULimit.DeepCopy()
		cpu = &cpuCopy
	}
	if c.maxMemLimit != nil {
		memCopy := c.maxMemLimit.DeepCopy()
		mem = &memCopy
	}
	return cpu, mem
}

func (c *Configer) GetMaxPVCSize() *resource.Quantity {
	c.m.RLock()
	defer c.m.RUnlock()

	if c.maxPvcSize != nil {
		pvcSize := c.maxPvcSize.DeepCopy()
		return &pvcSize
	}
	return nil
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
