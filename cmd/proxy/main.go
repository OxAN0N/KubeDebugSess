package main

import (
	"flag"
	"log"
	"net/http"

	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"

	debugv1alpha1 "github.com/OxAN0N/KubeDebugSess/api/v1alpha1"
	"github.com/OxAN0N/KubeDebugSess/internal/proxy"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
)

func main() {
	var listenAddr string
	flag.StringVar(&listenAddr, "listen-addr", ":8080", "The address to listen on for HTTP requests.")
	flag.Parse()

	// Load Kubernetes configuration
	cfg, err := config.GetConfig()
	if err != nil {
		log.Fatalf("Failed to get kubeconfig: %v", err)
	}

	// Create a standard clientset for CoreV1 operations (attach)
	clientset, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		log.Fatalf("Failed to create clientset: %v", err)
	}

	// --- 수정된 부분 ---
	// Create a new scheme and add the default Kubernetes types and our custom type to it.
	scheme := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(scheme)
	_ = debugv1alpha1.AddToScheme(scheme)
	// --- ---

	// Create a controller-runtime client that knows about our custom resources.
	k8sClient, err := client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		log.Fatalf("Failed to create controller-runtime client: %v", err)
	}

	// Create and register the proxy server
	proxyServer := proxy.NewServer(clientset, cfg, k8sClient)
	http.Handle("/attach", proxyServer)

	log.Printf("Starting debug proxy server on %s", listenAddr)
	if err := http.ListenAndServe(listenAddr, nil); err != nil {
		log.Fatalf("Failed to start server: %v", err)
	}
}
