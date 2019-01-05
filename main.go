package main

import (
	"crypto/tls"
	"net/http"
	"os"
	"strings"

	"github.com/alecthomas/kingpin"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	ver "github.com/prometheus/common/version"
	log "github.com/sirupsen/logrus"
)

var (
	version string
)

func main() {
	ver.Version = version

	app := kingpin.New("resource-requests-admission-controller", "Validates Statefulset,Deployment,Pod resource requests.")

	app.Version(ver.Print("resource-requests-admission-controller"))
	app.HelpFlag.Short('h')

	certFile := app.Flag("tls-cert-file", "").Envar("TLS_CERT_FILE").Required().String()
	keyFile := app.Flag("tls-private-key-file", "").Envar("TLS_KEY_FILE").Required().String()

	excludeNs := app.Flag("exclude-namespaces", "Bypasses resources in namespaces.Example: kube-system,default").Envar("EXCLUDE_NAMESPACES").Strings()
	excludeNames := app.Flag("exclude-names", "Bypasses resources with given name and namespace. Example: pod-name.kube-system").Envar("EXCLUDE_NAMES").Strings()

	logLevel := app.Flag("log.level", "Log level.").Envar("LOG_LEVEL").
		Default("info").Enum("error", "warn", "info", "debug")
	logFormat := app.Flag("log.format", "Log format.").Envar("LOG_FORMAT").
		Default("text").Enum("text", "json")

	addr := app.Flag("addr", "Server address which will receive AdmissionReview requests.").Envar("ADDR").Default("0.0.0.0:8443").String()
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

	excludeNsMap := make(map[string]struct{})
	for _, ns := range *excludeNs {
		excludeNsMap[ns] = struct{}{}
	}

	excludeNameMap := make(map[NameNamespace]struct{})
	for _, nameNs := range *excludeNames {
		resp := strings.Split(nameNs, ".")
		if len(resp) != 2 {
			log.Fatalf("could not parse excluded resource name: %s, resource name must include namespace. Example: pod-name.namespace", nameNs)
		}

		excludeNameMap[NameNamespace{
			Name:      resp[0],
			Namespace: resp[1],
		}] = struct{}{}
	}

	rra := &ResourceRequestsAdmission{
		ExcludedNames:      excludeNameMap,
		ExcludedNamespaces: excludeNsMap,
	}

	cert, err := tls.LoadX509KeyPair(*certFile, *keyFile)
	if err != nil {
		log.WithError(err).Fatal("unable to load certificates")
	}

	mux := http.NewServeMux()

	mux.Handle("/metrics", promhttp.Handler())
	mux.Handle("/", &AdmissionControllerServer{
		AdmissionController: rra,
		Decoder:             codecs.UniversalDeserializer(),
	})

	server := &http.Server{
		Handler: mux,
		Addr:    *addr,
		TLSConfig: &tls.Config{
			Certificates: []tls.Certificate{cert},
		},
	}

	log.Infof("app started, exluding namespaces: %q, names: %q, listening on: %s ", *excludeNs, *excludeNames, *addr)
	if err := server.ListenAndServeTLS("", ""); err != nil {
		log.WithError(err).Fatal("unable to start http server")
	}
}
