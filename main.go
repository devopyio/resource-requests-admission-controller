package main

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/alecthomas/kingpin"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	ver "github.com/prometheus/common/version"
	log "github.com/sirupsen/logrus"
)

var (
	version string
)

func main() {
	ver.Version = version

	app := kingpin.New("resource-requests-admission-controller", "Validates Statefulset,Deployment,Daemoneset,Pod resource requests and limits")

	app.Version(ver.Print("resource-requests-admission-controller"))
	app.HelpFlag.Short('h')

	certFile := app.Flag("tls-cert-file", "").Envar("TLS_CERT_FILE").Required().String()
	keyFile := app.Flag("tls-private-key-file", "").Envar("TLS_KEY_FILE").Required().String()
	configFile := app.Flag("config-file", "File path to the config").Envar("CONFIG_FILE").Required().String()
	refreshInterval := app.Flag("refresh-interval", "Refresh interval in if no file change happens.").Envar("CONFIG_FILE").Default("5m").Duration()
	logLevel := app.Flag("log.level", "Log level.").Envar("LOG_LEVEL").
		Default("info").Enum("error", "warn", "info", "debug")
	logFormat := app.Flag("log.format", "Log format.").Envar("LOG_FORMAT").
		Default("text").Enum("text", "json")

	addr := app.Flag("addr", "Server address which will receive AdmissionReview requests.").Envar("ADDR").Default("0.0.0.0:8443").String()
	opsAddr := app.Flag("ops-addr", "Server address which will serve prometheus metrics.").Envar("PROM_ADDR").Default("0.0.0.0:8090").String()

	kingpin.MustParse(app.Parse(os.Args[1:]))

	switch strings.ToLower(*logLevel) {
	case "error":
		log.SetLevel(log.ErrorLevel)
	case "warn":
		log.SetLevel(log.WarnLevel)
	case "info":
		log.SetLevel(log.InfoLevel)
	case "debug":
		log.SetLevel(log.DebugLevel)
	}

	switch strings.ToLower(*logFormat) {
	case "json":
		log.SetFormatter(&log.JSONFormatter{})
	case "text":
		log.SetFormatter(&log.TextFormatter{DisableColors: true})
	}
	log.SetOutput(os.Stdout)

	configer, err := NewConfiger(*configFile, *refreshInterval)
	if err != nil {
		log.WithError(err).Fatal("unable to load config file: %s", *configFile)
	}
	defer configer.Close()

	rra := New(configer)

	cert, err := tls.LoadX509KeyPair(*certFile, *keyFile)
	if err != nil {
		log.WithError(err).Fatal("unable to load certificates")
	}

	opsServer := http.Server{
		Addr:    fmt.Sprintf(*opsAddr),
		Handler: promhttp.Handler(),
	}
	go func() {
		opsErr := opsServer.ListenAndServe()
		switch opsErr {
		case http.ErrServerClosed:
			log.WithError(opsErr).Warn("ops server shutdown")
		default:
			log.WithError(opsErr).Panic("unable to start ops http server")
		}
	}()
	defer func() {
		err := opsServer.Shutdown(context.Background())
		if err != nil {
			log.WithError(err).Error("unable to shutdown ops http server")
		}
	}()

	log.Infof("app started,listening on: %s, prometheus on: %s", *addr, *opsAddr)
	server := &http.Server{
		Handler: prometheus.InstrumentHandler("admission", &AdmissionControllerServer{
			AdmissionController: rra,
			Decoder:             codecs.UniversalDeserializer(),
		}),
		Addr: *addr,
		TLSConfig: &tls.Config{
			Certificates: []tls.Certificate{cert},
		},
	}
	go func() {
		err := server.ListenAndServeTLS("", "")
		switch err {
		case http.ErrServerClosed:
			log.WithError(err).Warn("ops server shutdown")
		default:
			log.WithError(err).Panic("unable to start http server")
		}
	}()

	defer func() {
		err := server.Shutdown(context.Background())
		if err != nil {
			log.WithError(err).Error("unable to shutdown http server")
		}
	}()

	waitForShutdown()
}

func waitForShutdown() {
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	<-sigChan
	log.Warn("shutting down")
}
