package cluster

import (
	"context"
	"crypto/tls"
	"log"
	"net"
	"net/http"
	"path/filepath"

	"github.com/rancher/dynamiclistener"
	"github.com/rancher/dynamiclistener/factory"
	"github.com/rancher/dynamiclistener/storage/file"
	"github.com/rancher/dynamiclistener/storage/kubernetes"
	"github.com/rancher/dynamiclistener/storage/memory"
	"github.com/rancher/k3s/pkg/daemons/config"
	"github.com/rancher/k3s/pkg/version"
	"github.com/rancher/wrangler-api/pkg/generated/controllers/core"
	"github.com/sirupsen/logrus"
)

func (c *Cluster) newListener(ctx context.Context) (net.Listener, http.Handler, error) {
	tcp, err := dynamiclistener.NewTCPListener(c.config.BindAddress, c.config.SupervisorPort)
	if err != nil {
		return nil, nil, err
	}

	cert, key, err := factory.LoadCerts(c.runtime.ServerCA, c.runtime.ServerCAKey)
	if err != nil {
		return nil, nil, err
	}

	storage := tlsStorage(ctx, c.config.DataDir, c.runtime)
	return dynamiclistener.NewListener(tcp, storage, cert, key, dynamiclistener.Config{
		CN:           version.Program,
		Organization: []string{version.Program},
		TLSConfig: &tls.Config{
			ClientAuth:   tls.RequestClientCert,
			MinVersion:   c.config.TLSMinVersion,
			CipherSuites: c.config.TLSCipherSuites,
		},
		SANs:                append(c.config.SANs, "localhost", "kubernetes", "kubernetes.default", "kubernetes.default.svc."+c.config.ClusterDomain),
		ExpirationDaysCheck: config.CertificateRenewDays,
	})
}

func (c *Cluster) initClusterAndHTTPS(ctx context.Context) error {
	l, handler, err := c.newListener(ctx)
	if err != nil {
		return err
	}

	handler, err = c.getHandler(handler)
	if err != nil {
		return err
	}

	// Config the cluster database and allow it to add additional request handlers
	handler, err = c.initClusterDB(ctx, handler)
	if err != nil {
		return err
	}

	server := http.Server{
		Handler:  handler,
		ErrorLog: log.New(logrus.StandardLogger().Writer(), "Cluster-Http-Server ", log.LstdFlags),
	}

	go func() {
		err := server.Serve(l)
		logrus.Fatalf("server stopped: %v", err)
	}()

	go func() {
		<-ctx.Done()
		server.Shutdown(context.Background())
	}()

	return nil
}

func tlsStorage(ctx context.Context, dataDir string, runtime *config.ControlRuntime) dynamiclistener.TLSStorage {
	fileStorage := file.New(filepath.Join(dataDir, "tls/dynamic-cert.json"))
	cache := memory.NewBacked(fileStorage)
	return kubernetes.New(ctx, func() *core.Factory {
		return runtime.Core
	}, "kube-system", ""+version.Program+"-serving", cache)
}
