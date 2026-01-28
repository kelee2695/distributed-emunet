package main

import (
	"crypto/tls"
	"flag"
	"net/http"
	"os"
	"time"

	_ "k8s.io/client-go/plugin/pkg/client/auth"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/webhook"

	emunetv1 "github.com/emunet/emunet-operator/api/v1"
	"github.com/emunet/emunet-operator/internal/api"
	"github.com/emunet/emunet-operator/internal/controller"
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(emunetv1.AddToScheme(scheme))
}

func main() {
	var apiAddr string
	var enableLeaderElection bool
	var probeAddr string
	var enableHTTP2 bool
	var tlsOpts []func(*tls.Config)
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "The address of the probe endpoint binds to.")
	flag.StringVar(&apiAddr, "api-bind-address", ":12345", "The address of the REST API endpoint binds to.")
	flag.BoolVar(&enableLeaderElection, "leader-elect", false,
		"Enable leader election for controller manager. "+
			"Enabling this will ensure there is only one active controller manager.")
	flag.BoolVar(&enableHTTP2, "enable-http2", false,
		"If set, HTTP/2 will be enabled for the webhook server")
	opts := zap.Options{
		Development: true,
	}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))

	disableHTTP2 := func(c *tls.Config) {
		setupLog.Info("disabling http/2")
		c.NextProtos = []string{"http/1.1"}
	}

	if !enableHTTP2 {
		tlsOpts = append(tlsOpts, disableHTTP2)
	}

	webhookTLSOpts := tlsOpts
	webhookServerOptions := webhook.Options{
		TLSOpts: webhookTLSOpts,
	}

	webhookServer := webhook.NewServer(webhookServerOptions)

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:                 scheme,
		WebhookServer:          webhookServer,
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         enableLeaderElection,
		LeaderElectionID:       "dcc08e36.emunet.io",
	})
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	if err := (&controller.EmuNetReconciler{
		Client:       mgr.GetClient(),
		Scheme:       mgr.GetScheme(),
		PodInfoStore: controller.GetGlobalPodInfoStore(),
		NodeName:     getNodeName(),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "EmuNet")
		os.Exit(1)
	}

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up health check")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up ready check")
		os.Exit(1)
	}

	apiServer := api.NewServer()
	go func() {
		setupLog.Info("starting REST API server", "address", apiAddr)

		server := &http.Server{
			Addr:         apiAddr,
			Handler:      apiServer.GetRouter(),
			ReadTimeout:  60 * time.Second,
			WriteTimeout: 60 * time.Second,
			IdleTimeout:  120 * time.Second,
		}

		if err := server.ListenAndServe(); err != nil {
			setupLog.Error(err, "problem running REST API server")
			os.Exit(1)
		}
	}()

	setupLog.Info("starting manager")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}

func getNodeName() string {
	hostname, err := os.Hostname()
	if err != nil {
		return ""
	}
	return strings.ToLower(hostname)
}
